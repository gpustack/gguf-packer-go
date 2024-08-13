package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/chrismellard/docker-credential-acr-env/pkg/credhelper"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/authn/github"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	conreg "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/cache"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
	"github.com/gpustack/gguf-packer-go/util/osx"
	"github.com/gpustack/gguf-packer-go/util/ptr"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

func pull(app string) *cobra.Command {
	var (
		insecure bool
		force    bool
	)

	c := &cobra.Command{
		Use:   "pull MODEL",
		Short: "Download a model from a registry.",
		Example: sprintf(`  # Download a model
  %[1]s pull gpustack/qwen2:latest

  # Force download a model from remote
  %[1]s pull gpustack/qwen2:latest --force`, app),
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) (err error) {
			model := args[0]

			var cos crane.Options
			{
				co := []crane.Option{
					getAuthnKeychainOption(),
				}
				if insecure {
					co = append(co, crane.Insecure)
				}
				cos = crane.GetOptions(co...)
			}

			rf, err := name.NewTag(model, cos.Name...)
			if err != nil {
				return fmt.Errorf("parsing model reference %q: %w", model, err)
			}

			mdp := getModelMetadataStorePath(rf)
			if osx.ExistsLink(mdp) && !force {
				return nil
			}

			var img conreg.Image
			{
				var rd *remote.Descriptor
				rd, err = remote.Get(rf, cos.Remote...)
				if err != nil {
					return fmt.Errorf("getting model remote %q: %w", rf.Name(), err)
				}
				img, err = retrieveOCIImage(rd)
				if err != nil {
					return err
				}
			}
			cfp, lsp, err := getModelConfigAndLayersStorePaths(img)
			if err != nil {
				return err
			}

			// Link.
			if !force {
				if osx.ExistsLink(mdp) {
					cfpActual, err := os.Readlink(mdp)
					if err != nil {
						return fmt.Errorf("reading link %s: %w", mdp, err)
					}
					if cfpActual == cfp {
						return nil
					}
					// Create a tombstone.
					mdpTomb := filepath.Join(filepath.Dir(mdp), oldPrefix+filepath.Base(cfpActual))
					if err = os.Rename(mdp, mdpTomb); err != nil {
						return fmt.Errorf("renaming link %s: %w", mdp, err)
					}
					// Restore a tombstone if exists.
					mdpTomb = filepath.Join(filepath.Dir(mdp), oldPrefix+filepath.Base(cfp))
					if osx.ExistsLink(mdpTomb) {
						if err = os.Rename(mdpTomb, mdp); err == nil {
							return nil
						}
						if err = os.Remove(mdpTomb); err != nil && !os.IsNotExist(err) {
							return fmt.Errorf("force removing link %s: %w", mdpTomb, err)
						}
					}
				}
			}
			defer func() {
				if err != nil {
					return
				}
				if err = osx.ForceSymlink(cfp, mdp); err != nil {
					err = fmt.Errorf("link metadata %s from %s: %w", mdp, cfp, err)
					return
				}
			}()

			// Retrieve and save config.
			_, cfBs, err := retrieveConfigByOCIImage(img)
			if err != nil {
				return err
			}
			if err = osx.WriteFile(cfp, cfBs, 0644); err != nil {
				return fmt.Errorf("writing config file: %w", err)
			}

			// Extract and flatten layers.
			ls, err := img.Layers()
			if err != nil {
				return fmt.Errorf("retrieving image layers: %w", err)
			}
			if err = os.MkdirAll(lsp, 0755); err != nil {
				return fmt.Errorf("creating layers directory: %w", err)
			}

			for i := range ls {
				s, err := ls[i].Size()
				if err != nil {
					return fmt.Errorf("getting layer size: %w", err)
				}
				d, err := ls[i].Digest()
				if err != nil {
					return fmt.Errorf("getting layer digest: %w", err)
				}
				l, err := ls[i].Uncompressed()
				if err != nil {
					return fmt.Errorf("reading layer contents: %w", err)
				}
				pb := progressbar.NewOptions64(s,
					progressbar.OptionSetDescription(sprintf("[%d/%d]", i+1, len(ls))),
					progressbar.OptionSetWriter(c.OutOrStderr()),
					progressbar.OptionSetWidth(30),
					progressbar.OptionThrottle(65*time.Millisecond),
					progressbar.OptionShowBytes(true),
					progressbar.OptionShowCount(),
					progressbar.OptionSetPredictTime(false),
					progressbar.OptionSpinnerType(14),
					progressbar.OptionSetRenderBlankState(true))
				if _, err = archive.Apply(c.Context(), lsp, ptr.To(progressbar.NewReader(l, pb)), archive.WithNoSameOwner()); err != nil {
					_ = l.Close()
					_ = pb.Clear()
					return fmt.Errorf("extracting layer %q: %w", d, err)
				}
				_ = pb.Clear()
				if cl, ok := l.(cacheLayerReadCloser); ok {
					if err = cl.Complete(); err != nil {
						return fmt.Errorf("completing layer %q: %w", d, err)
					}
				}
			}

			return nil
		},
	}
	c.Flags().BoolVar(&insecure, "insecure", insecure, "Allow model references to be fetched without TLS.")
	c.Flags().BoolVar(&force, "force", force, "Always pull the model from the registry.")
	return c
}

func getAuthnKeychainOption() crane.Option {
	mc := authn.NewMultiKeychain(
		authn.DefaultKeychain,
		google.Keychain,
		github.Keychain,
		authn.NewKeychainFromHelper(ecr.NewECRHelper(ecr.WithLogger(io.Discard))),
		authn.NewKeychainFromHelper(credhelper.NewACRCredentialsHelper()))
	return crane.WithAuthFromKeychain(mc)
}

const (
	dockerRegPrefix = "index.docker.io/"
	oldPrefix       = ".old."
)

func getModelMetadataStorePath(ref name.Reference) (mdp string) {
	const dockerRegAliasPrefix = "docker.io/"
	rn := ref.Name()
	if strings.HasPrefix(rn, dockerRegAliasPrefix) {
		rn = dockerRegPrefix + strings.TrimPrefix(rn, dockerRegAliasPrefix)
	}
	rn = strings.ReplaceAll(rn, ":", "/")
	mdp = filepath.Join(getModelsMetadataStorePath(), filepath.Clean(rn))
	return mdp
}

func getModelConfigAndLayersStorePaths(img conreg.Image) (cfp, lsp string, err error) {
	cn, err := img.ConfigName()
	if err != nil {
		return "", "", fmt.Errorf("getting config name: %w", err)
	}
	cfp = filepath.Join(getModelsConfigStorePath(), cn.Algorithm, cn.Hex)
	lsp = filepath.Join(getModelsLayersStorePath(), cn.Algorithm, cn.Hex)
	return cfp, lsp, err
}

func retrieveOCIImage(rd *remote.Descriptor) (img conreg.Image, err error) {
	if rd.MediaType.IsIndex() {
		idx, err := rd.ImageIndex()
		if err != nil {
			return nil, fmt.Errorf("getting model index: %w", err)
		}
		idxMs, err := idx.IndexManifest()
		if err != nil {
			return nil, fmt.Errorf("getting model index manifest: %w", err)
		}
		if len(idxMs.Manifests) == 0 {
			return nil, errors.New("empty model index")
		}
		img, err = idx.Image(idxMs.Manifests[0].Digest)
		if err != nil {
			return nil, fmt.Errorf("getting model from index: %w", err)
		}
	} else {
		img, err = rd.Image()
		if err != nil {
			return nil, fmt.Errorf("getting model: %w", err)
		}
	}
	img = cache.Image(img, cacheLayers(getBlobsStorePath()))
	return img, nil
}

func retrieveConfigByOCIImage(img conreg.Image) (cf specs.Image, cfBs []byte, err error) {
	cfBs, err = img.RawConfigFile()
	if err != nil {
		return cf, cfBs, fmt.Errorf("getting config: %w", err)
	}
	if err = json.Unmarshal(cfBs, &cf); err != nil {
		return cf, cfBs, fmt.Errorf("unmarshalling config: %w", err)
	}
	if !isConfigAvailable(&cf) {
		return cf, cfBs, errors.New("unavailable model config")
	}
	return cf, cfBs, nil
}

func isConfigAvailable(cf *specs.Image) bool {
	return cf.Config.Model != nil && len(cf.Config.Model.Header.MetadataKV) != 0 && len(cf.Config.Model.TensorInfos) != 0
}

func getBlobsStorePath() string {
	return filepath.Join(storePath, "blobs")
}

type cacheLayers string

func (c cacheLayers) Put(l conreg.Layer) (conreg.Layer, error) {
	digest, err := l.Digest()
	if err != nil {
		return nil, err
	}
	diffID, err := l.DiffID()
	if err != nil {
		return nil, err
	}
	cl := &cacheLayer{
		Layer:      l,
		DigestPath: c.getLayerPath(digest),
		DiffIDPath: c.getLayerPath(diffID),
	}
	return cl, nil
}

func (c cacheLayers) Get(h conreg.Hash) (conreg.Layer, error) {
	l, err := tarball.LayerFromFile(c.getLayerPath(h))
	if os.IsNotExist(err) {
		return nil, cache.ErrNotFound
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		if err := c.Delete(h); err != nil {
			return nil, err
		}
		return nil, cache.ErrNotFound
	}
	return l, err
}

func (c cacheLayers) Delete(h conreg.Hash) error {
	err := os.Remove(c.getLayerPath(h))
	if os.IsNotExist(err) {
		return cache.ErrNotFound
	}
	return err
}

func (c cacheLayers) getLayerPath(h conreg.Hash) string {
	return filepath.Join(string(c), h.Algorithm, h.Hex)
}

type cacheLayer struct {
	conreg.Layer

	DigestPath string
	DiffIDPath string
}

func (c *cacheLayer) Compressed() (io.ReadCloser, error) {
	tmp := c.DigestPath + ".tmp"

	f, err := osx.CreateFile(tmp, 0700)
	if err != nil {
		return nil, err
	}
	rc, err := c.Layer.Compressed()
	if err != nil {
		return nil, err
	}
	cr := cacheLayerReadCloser{
		Reader:   io.TeeReader(rc, f),
		Closers:  []io.Closer{rc, f},
		Complete: func() error { return os.Rename(tmp, c.DigestPath) },
	}
	return cr, nil
}

func (c *cacheLayer) Uncompressed() (io.ReadCloser, error) {
	tmp := c.DigestPath + ".tmp"

	f, err := osx.CreateFile(tmp, 0700)
	if err != nil {
		return nil, err
	}
	rc, err := c.Layer.Uncompressed()
	if err != nil {
		return nil, err
	}
	cr := cacheLayerReadCloser{
		Reader:   io.TeeReader(rc, f),
		Closers:  []io.Closer{rc, f},
		Complete: func() error { return os.Rename(tmp, c.DiffIDPath) },
	}
	return cr, nil
}

type cacheLayerReadCloser struct {
	io.Reader

	Closers  []io.Closer
	Complete func() error
}

func (c cacheLayerReadCloser) Close() (err error) {
	for i := range c.Closers {
		lastErr := c.Closers[i].Close()
		if err == nil {
			err = lastErr
		}
	}
	return err
}
