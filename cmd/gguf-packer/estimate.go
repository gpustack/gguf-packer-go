package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/gpustack/gguf-packer-go/util/ptr"
	ggufparser "github.com/gpustack/gguf-parser-go"
	"github.com/spf13/cobra"
)

func estimate(app string) *cobra.Command {
	var (
		insecure           bool
		force              bool
		ctxSize            = -1
		logicalBatchSize   = 2048
		physicalBatchSize  = 512
		parallelSize       = 1
		cacheKeyType       = "f16"
		cacheValueType     = "f16"
		noKVOffload        bool
		flashAttention     bool
		platformFootprint  = "150,250"
		noMMap             bool
		offloadLayers      = -1
		offloadLayersDraft = -1
		offloadLayersStep  uint64
		json               bool
	)

	c := &cobra.Command{
		Use:   "estimate MODEL",
		Short: "Estimate the model memory usage.",
		Example: sprintf(`  # Estimate the model memory usage
  %s estimate gpustack/qwen2:latest

  # Estimate the model memory usage from remote
  %[1]s estimate gpustack/qwen2:latest --force

  # Estimate the model memory usage with overrided flags
  %[1]s estimate gpustack/qwen2:latest --gpu-layers 10 --flash-attention

  # Estimate the model memory usage step by step
  %[1]s estimate gpustack/qwen2:latest --offload-layers-step 1`, app),
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
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

			cf, err := retrieveConfigByOCIReference(force, rf, cos.Remote...)
			if err != nil {
				return err
			}

			// Retrieve args.
			var (
				mopts []ggufparser.LLaMACppUsageEstimateOption

				rawNoMMap             *bool
				rawOffloadLayers      *int
				rawOffloadLayersDraft *int
			)
			for i, s, cmd := 0, len(cf.Config.Cmd), cf.Config.Cmd; i < s; i++ {
				switch cmd[i] {
				case "-c", "--ctx-size":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					mopts = append(mopts, ggufparser.WithContextSize(int32(v)))
				case "-b", "--batch-size":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					mopts = append(mopts, ggufparser.WithLogicalBatchSize(int32(v)))
				case "-ub", "--ubatch-size":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					mopts = append(mopts, ggufparser.WithPhysicalBatchSize(int32(v)))
				case "-np", "--parallel":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					mopts = append(mopts, ggufparser.WithParallelSize(int32(v)))
				case "-nkvo", "--no-kv-offload":
					mopts = append(mopts, ggufparser.WithoutOffloadKVCache())
				case "-ctk", "--cache-type-k":
					if i+1 >= s {
						continue
					}
					i++
					mopts = append(mopts, ggufparser.WithCacheKeyType(toGGMLType(cmd[i])))
				case "-ctv", "--cache-type-v":
					if i+1 >= s {
						continue
					}
					i++
					mopts = append(mopts, ggufparser.WithCacheValueType(toGGMLType(cmd[i])))
				case "-fa", "--flash-attn":
					mopts = append(mopts, ggufparser.WithFlashAttention())
				case "--no-mmap":
					rawNoMMap = ptr.To(true)
				case "-ngl", "--gpu-layers":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					rawOffloadLayers = ptr.To(int(v))
				case "-ngld", "--gpu-layers-draft":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					rawOffloadLayersDraft = ptr.To(int(v))
				}
			}

			// Override.
			if ctxSize > 0 {
				mopts = append(mopts, ggufparser.WithContextSize(int32(ctxSize)))
			}
			if logicalBatchSize > 0 {
				mopts = append(mopts, ggufparser.WithLogicalBatchSize(int32(logicalBatchSize)))
			}
			if physicalBatchSize > 0 {
				if physicalBatchSize > logicalBatchSize {
					return errors.New("--ubatch-size must be less than or equal to --batch-size")
				}
				mopts = append(mopts, ggufparser.WithPhysicalBatchSize(int32(physicalBatchSize)))
			}
			if parallelSize > 0 {
				mopts = append(mopts, ggufparser.WithParallelSize(int32(parallelSize)))
			}
			if cacheKeyType != "" {
				mopts = append(mopts, ggufparser.WithCacheKeyType(toGGMLType(cacheKeyType)))
			}
			if cacheValueType != "" {
				mopts = append(mopts, ggufparser.WithCacheValueType(toGGMLType(cacheValueType)))
			}
			if noKVOffload {
				mopts = append(mopts, ggufparser.WithoutOffloadKVCache())
			}
			if flashAttention {
				mopts = append(mopts, ggufparser.WithFlashAttention())
			}
			if rawNoMMap != nil && !c.Flags().Changed("no-mmap") {
				noMMap = *rawNoMMap
			}
			if rawOffloadLayers != nil && !c.Flags().Changed("gpu-layers") {
				offloadLayers = *rawOffloadLayers
			}
			if rawOffloadLayersDraft != nil && !c.Flags().Changed("gpu-layers-draft") {
				offloadLayersDraft = *rawOffloadLayersDraft
			}

			// Estimate.
			if p := cf.Config.Projector; p != nil {
				popts := mopts[:len(mopts):len(mopts)]
				pe := p.EstimateLLaMACppUsage(popts...)
				mopts = append(mopts, ggufparser.WithMultimodalProjector(&pe))
			}
			if d := cf.Config.Drafter; d != nil {
				dopts := mopts[:len(mopts):len(mopts)]
				if offloadLayersDraft >= 0 {
					dopts = append(dopts, ggufparser.WithOffloadLayers(uint64(offloadLayersDraft)))
				}
				de := d.EstimateLLaMACppUsage(dopts...)
				mopts = append(mopts, ggufparser.WithDrafter(&de))
			}
			// TODO adapter.
			if offloadLayers >= 0 {
				mopts = append(mopts, ggufparser.WithOffloadLayers(uint64(offloadLayers)))
			}
			e := cf.Config.Model.EstimateLLaMACppUsage(mopts...)

			var (
				mmap                      = !noMMap
				platformRAM, platformVRAM uint64
			)
			{
				if platformFootprint != "" {
					parts := strings.Split(platformFootprint, ",")
					if len(parts) == 2 {
						if v, err := strconv.ParseUint(parts[0], 10, 64); err == nil {
							platformRAM = v * 1024 * 1024
						}
						if v, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
							platformVRAM = v * 1024 * 1024
						}
					}
				}
			}
			es := e.Summarize(mmap, platformRAM, platformVRAM)
			switch {
			case offloadLayersStep > e.OffloadLayers:
				offloadLayersStep = e.OffloadLayers
			case offloadLayersStep <= 0:
				offloadLayersStep = e.OffloadLayers
			}
			if offloadLayersStep < e.OffloadLayers {
				cnt := e.OffloadLayers/offloadLayersStep + 1
				if e.OffloadLayers%offloadLayersStep != 0 || e.FullOffloaded {
					cnt++
				}
				ess := make([]ggufparser.LLaMACppUsageEstimateMemorySummary, cnt)
				var wg sync.WaitGroup
				for i := 0; i < cap(ess); i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						mopts := mopts[:len(mopts):len(mopts)]
						mopts = append(mopts, ggufparser.WithOffloadLayers(uint64(i)*offloadLayersStep))
						ess[i] = cf.Config.Model.EstimateLLaMACppUsage(mopts...).SummarizeMemory(mmap, platformRAM, platformVRAM)
					}(i)
				}
				wg.Wait()
				ess[cap(ess)-1] = es.Memory[0]
				es.Memory = ess
			}

			w := c.OutOrStdout()
			if json {
				jprint(w, es)
				return nil
			}

			var (
				hd  []string
				mg  []int
				bds [][]string
			)
			if e.Architecture != "clip" {
				hd = []string{
					"Arch",
					"Context Size",
					"Batch Size (L / P)",
					"Flash Attention",
					"MMap Support",
					"Embedding Only",
					"Offload Layers",
					"Full Offloaded",
					"UMA (RAM + VRAM)",
					"NonUMA RAM",
					"NonUMA VRAM",
				}
				mg = []int{0, 1, 2, 3, 4, 7}
				bds = make([][]string, len(es.Memory))
				for i := range es.Memory {
					bds[i] = []string{
						sprintf(es.Architecture),
						sprintf(es.ContextSize),
						sprintf("%d / %d", es.LogicalBatchSize, es.PhysicalBatchSize),
						sprintf(es.FlashAttention),
						sprintf(!es.NoMMap),
						sprintf(es.EmbeddingOnly),
						sprintf(tenary(es.Memory[i].FullOffloaded, sprintf("%d (%d + 1)",
							es.Memory[i].OffloadLayers, es.Memory[i].OffloadLayers-1), es.Memory[i].OffloadLayers)),
						sprintf(tenary(es.Memory[i].FullOffloaded, "Yes", "No")),
						sprintf("%s + %s = %s", es.Memory[i].UMA.RAM, es.Memory[i].UMA.VRAM, es.Memory[i].UMA.RAM+es.Memory[i].UMA.VRAM),
						sprintf(es.Memory[i].NonUMA.RAM),
						sprintf(es.Memory[i].NonUMA.VRAM),
					}
				}
			} else {
				hd = []string{
					"Arch",
					"Offload Layers",
					"Full Offloaded",
					"(V)RAM",
				}
				bds = [][]string{
					{
						sprintf(es.Architecture),
						sprintf(es.Memory[0].OffloadLayers),
						sprintf(tenary(es.Memory[0].FullOffloaded, "Yes", "No")),
						sprintf(max(es.Memory[0].UMA.RAM, es.Memory[0].UMA.VRAM)),
					},
				}
			}
			tfprint(c.OutOrStdout(), true, hd, mg, bds...)

			return nil
		},
	}
	c.Flags().BoolVar(&insecure, "insecure", insecure, "Allow model references to be fetched without TLS.")
	c.Flags().BoolVar(&force, "force", force, "Always estimate the model from the registry.")
	c.Flags().IntVar(&ctxSize, "ctx-size", ctxSize, "Specify the context size.")
	c.Flags().IntVar(&logicalBatchSize, "batch-size", logicalBatchSize, "Specify the logical batch size.")
	c.Flags().IntVar(&physicalBatchSize, "ubatch-size", physicalBatchSize, "Specify the physical batch size.")
	c.Flags().IntVar(&parallelSize, "parallel", parallelSize, "Specify the parallel size.")
	c.Flags().StringVar(&cacheKeyType, "cache-type-k", cacheKeyType, "Specify the cache key type.")
	c.Flags().StringVar(&cacheValueType, "cache-type-v", cacheValueType, "Specify the cache value type.")
	c.Flags().BoolVar(&noKVOffload, "no-kv-offload", noKVOffload, "Disable the key-value offload.")
	c.Flags().BoolVar(&flashAttention, "flash-attn", flashAttention, "Enable the flash attention.")
	c.Flags().StringVar(&platformFootprint, "platform-footprint", platformFootprint, "Specify the platform footprint(RAM,VRAM) in MiB.")
	c.Flags().BoolVar(&noMMap, "no-mmap", noMMap, "Disable the memory mapping.")
	c.Flags().IntVar(&offloadLayers, "gpu-layers", offloadLayers, "Specify the offload layers.")
	c.Flags().IntVar(&offloadLayersDraft, "gpu-layers-draft", offloadLayersDraft, "Specify the offload layers draft.")
	c.Flags().Uint64Var(&offloadLayersStep, "gpu-layers-step", offloadLayersStep, "Specify the offload layers step.")
	c.Flags().BoolVar(&json, "json", json, "Output as JSON.")
	return c
}

func toGGMLType(s string) ggufparser.GGMLType {
	t := ggufparser.GGMLTypeF16
	switch s {
	case "f32":
		t = ggufparser.GGMLTypeF32
	case "f16":
		t = ggufparser.GGMLTypeF16
	case "q8_0":
		t = ggufparser.GGMLTypeQ8_0
	case "q4_0":
		t = ggufparser.GGMLTypeQ4_0
	case "q4_1":
		t = ggufparser.GGMLTypeQ4_1
	case "iq4_nl":
		t = ggufparser.GGMLTypeIQ4_NL
	case "q5_0":
		t = ggufparser.GGMLTypeQ5_0
	case "q5_1":
		t = ggufparser.GGMLTypeQ5_1
	}
	return t
}
