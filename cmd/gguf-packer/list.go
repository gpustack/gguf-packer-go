package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/gpustack/gguf-packer-go/util/mapx"
	"github.com/gpustack/gguf-packer-go/util/osx"
	"github.com/spf13/cobra"
)

func list(app string) *cobra.Command {
	var (
		fullID bool
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List all local models.",
		Example: sprintf(`  # List all local models
  %s list`, app),
		Args: cobra.ExactArgs(0),
		RunE: func(c *cobra.Command, args []string) error {
			var (
				hd = []string{
					"Name",
					"Tag",
					"ID",
					"Arch",
					"Params",
					"Bpw",
					"Type",
					"Usage",
					"Created",
					"Size",
				}
				mg []int
				bd [][]string
			)

			msdp := getModelsMetadataStorePath()
			if osx.ExistsDir(msdp) {
				_ = filepath.Walk(msdp, func(mdp string, info os.FileInfo, err error) error {
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

					img, err := retrieveConfigByPath(cfp)
					if err != nil {
						// Ignore invalid model metadata.
						return nil
					}

					mname := strings.TrimPrefix(filepath.Dir(mdp), msdp+string(filepath.Separator))
					mtag := filepath.Base(mdp)
					mid := filepath.Base(cfp)
					arch := img.Config.Model.Architecture
					params := img.Config.Model.Parameters
					bpw := img.Config.Model.BitsPerWeight
					fileType := img.Config.Model.FileType
					usage := mapx.Value(img.Config.Labels, "gguf.model.usage", "unknown")
					created := img.Created
					size := img.Config.Size

					bd = append(bd, []string{
						sprintf(tenary(strings.HasPrefix(mname, dockerRegPrefix), mname[16:], mname)),
						sprintf(tenary(strings.HasPrefix(mtag, oldPrefix), "<none>", mtag)),
						sprintf(tenary(fullID, mid, mid[:12])),
						sprintf(arch),
						sprintf(params),
						sprintf(bpw),
						sprintf(fileType),
						sprintf(usage),
						sprintf(tenary(created != nil, humanize.Time(*created), "unknown")),
						sprintf(size),
					})
					return nil
				})
			}

			tfprint(c.OutOrStdout(), false, hd, mg, bd...)
			return nil
		},
	}
	c.Flags().BoolVar(&fullID, "full-id", false, "Display full model ID.")
	return c
}
