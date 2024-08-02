package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"
)

func remove(app string) *cobra.Command {
	c := &cobra.Command{
		Use:   "remove MODEL [MODEL...]",
		Short: "Remove one or more local models.",
		Example: sprintf(`  # Remove local model by name
  %s remove gpustack/qwen2:latest

  # Remove local model by ID
  %s remove 6e76cdbc3a21`, app),
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			type queueItem struct {
				value string
				match bool
				err   error
			}
			q := make([]queueItem, len(args))

			for i := range args {
				if isIDAvailable(args[i]) {
					q[i].value = args[i]
					q[i].match = true
					continue
				}
				rf, err := name.NewTag(args[i])
				if err != nil {
					return fmt.Errorf("parsing model reference %q: %w", args[i], err)
				}
				q[i].value = getModelMetadataStorePath(rf)
			}

			type matchItem struct {
				mdp string
				cfp string
			}
			m := map[int][]matchItem{}

			// Process the candidates by name.
			for i := range q {
				if q[i].match {
					m[i] = nil
					continue
				}
				rp, err := os.Readlink(q[i].value)
				if err != nil {
					q[i].err = errors.New("model not found")
					continue
				}
				var mdp, cfp, lsp string
				{
					mdp = q[i].value
					cfp = rp
					lsp = convertConfigStorePathToLayersStorePath(cfp)
				}
				if err = os.RemoveAll(lsp); err != nil && !os.IsNotExist(err) {
					q[i].err = fmt.Errorf("removing layers: %w", err)
					continue
				}
				if err = os.Remove(cfp); err != nil && !os.IsNotExist(err) {
					q[i].err = fmt.Errorf("removing config: %w", err)
					continue
				}
				if err = os.Remove(mdp); err != nil && !os.IsNotExist(err) {
					q[i].err = fmt.Errorf("removing metadata: %w", err)
					continue
				}
			}

			// Process the candidates by ID.
			if len(m) != 0 {
				msdp := getModelsMetadataStorePath()
				_ = filepath.Walk(msdp, func(mdp string, info fs.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if info.IsDir() {
						if strings.HasPrefix(filepath.Base(mdp), ".") {
							return filepath.SkipDir
						}
						return nil
					}

					cfp, err := os.Readlink(mdp)
					if err != nil {
						// Ignore non-symbolic link.
						return nil
					}

					id := filepath.Base(cfp)
					for i := range m {
						if strings.HasPrefix(id, q[i].value) {
							m[i] = append(m[i], matchItem{mdp: mdp, cfp: cfp})
						}
					}
					return nil
				})
				for i := range m {
					switch s := len(m[i]); {
					case s == 0:
						q[i].err = errors.New("id not found")
					case s > 1:
						q[i].err = errors.New("id is not unique")
					default:
						var mdp, cfp, lsp string
						{
							mdp = m[i][0].mdp
							cfp = m[i][0].cfp
							lsp = convertConfigStorePathToLayersStorePath(cfp)
						}
						if err := os.RemoveAll(lsp); err != nil && !os.IsNotExist(err) {
							q[i].err = fmt.Errorf("removing layers: %w", err)
							continue
						}
						if err := os.Remove(cfp); err != nil && !os.IsNotExist(err) {
							q[i].err = fmt.Errorf("removing config: %w", err)
							continue
						}
						if err := os.Remove(mdp); err != nil && !os.IsNotExist(err) {
							q[i].err = fmt.Errorf("removing metadata: %w", err)
							continue
						}
					}
				}
			}

			we, wo := c.ErrOrStderr(), c.OutOrStderr()
			for i := range q {
				if err := q[i].err; err != nil {
					fprintf(we, "removing model %s failed: %v\n", args[i], err)
					continue
				}
				fprintf(wo, "removed model %s\n", args[i])
			}
			return nil
		},
	}
	return c
}

func convertConfigStorePathToLayersStorePath(cfp string) (lsp string) {
	return filepath.Join(getModelsLayersStorePath(), strings.TrimPrefix(cfp, getModelsConfigStorePath()))
}

func isIDAvailable(id string) bool {
	if len(id) < 12 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
			continue
		}
		return false
	}
	return true
}
