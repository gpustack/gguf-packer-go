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
		splitMode          = "layer"
		tensorSplit        string
		mainGPU            uint
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
  %s estimate gpustack/qwen2:0.5b-instruct

  # Estimate the model memory usage from remote
  %[1]s estimate gpustack/qwen2:0.5b-instruct --force

  # Estimate the model memory usage with overrided flags
  %[1]s estimate gpustack/qwen2:0.5b-instruct --gpu-layers 10 --flash-attention

  # Estimate the model memory usage step by step
  %[1]s estimate gpustack/qwen2:0.5b-instruct --offload-layers-step 1`, app),
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
				case "-sm", "--split-mode":
					if i+1 >= s {
						continue
					}
					i++
					switch cmd[i] {
					case "row":
						mopts = append(mopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeRow))
					case "none":
						mopts = append(mopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeNone))
					default:
						mopts = append(mopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeLayer))
					}
				case "-ts", "--tensor-split":
					if i+1 >= s {
						continue
					}
					i++
					var vf []float64
					{
						ss := strings.Split(cmd[i], ",")
						var vs float64
						vv := make([]float64, len(ss))
						vf = make([]float64, len(ss))
						for i, s := range ss {
							v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
							if err != nil {
								vv = nil
								vf = nil
								break
							}
							vs += v
							vv[i] = vs
						}
						for i, v := range vv {
							vf[i] = v / vs
						}
					}
					if len(vf) > 0 {
						mopts = append(mopts, ggufparser.WithTensorSplitFraction(vf))
					}
				case "-mg", "--main-gpu":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseUint(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					mopts = append(mopts, ggufparser.WithMainGPUIndex(int(v)))
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
			switch splitMode {
			case "row":
				mopts = append(mopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeRow))
			case "none":
				mopts = append(mopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeNone))
			default:
				mopts = append(mopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeLayer))
			}
			if tensorSplit != "" {
				ss := strings.Split(tensorSplit, ",")
				var vs float64
				vv := make([]float64, len(ss))
				vf := make([]float64, len(ss))
				for i, s := range ss {
					v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
					if err != nil {
						return errors.New("--tensor-split has invalid integer")
					}
					vs += v
					vv[i] = vs
				}
				for i, v := range vv {
					vf[i] = v / vs
				}
				mopts = append(mopts, ggufparser.WithTensorSplitFraction(vf))
				if mainGPU < uint(len(vv)) {
					mopts = append(mopts, ggufparser.WithMainGPUIndex(int(mainGPU)))
				} else {
					return errors.New("--main-gpu must be less than item size of --tensor-split")
				}
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
				hds [][]any
				bds [][]any
			)
			if e.Architecture != "clip" {
				hds = [][]any{
					{
						"Arch",
						"Context Size",
						"Batch Size (L / P)",
						"Flash Attention",
						"MMap Load",
						"Embedding Only",
						"Offload Layers",
						"Full Offloaded",
						"RAM",
						"RAM",
					},
					{
						"Arch",
						"Context Size",
						"Batch Size (L / P)",
						"Flash Attention",
						"MMap Load",
						"Embedding Only",
						"Offload Layers",
						"Full Offloaded",
						"UMA",
						"NonUMA",
					},
				}
				for i := range es.Memory[0].VRAMs {
					hds[0] = append(hds[0], fmt.Sprintf("VRAM %d", i), fmt.Sprintf("VRAM %d", i))
					hds[1] = append(hds[1], "UMA", "NonUMA")
				}

				bds = make([][]any, len(es.Memory))
				for i := range es.Memory {
					bds[i] = []any{
						sprintf(es.Architecture),
						sprintf(es.ContextSize),
						sprintf("%d / %d", es.LogicalBatchSize, es.PhysicalBatchSize),
						sprintf(tenary(es.FlashAttention, "Enabled", "Disabled")),
						sprintf(tenary(!es.NoMMap, "Supported", "Not Supported")),
						sprintf(tenary(es.EmbeddingOnly, "Yes", "No")),
						sprintf(tenary(es.Memory[i].FullOffloaded, sprintf("%d (%d + 1)",
							es.Memory[i].OffloadLayers, es.Memory[i].OffloadLayers-1), es.Memory[i].OffloadLayers)),
						sprintf(tenary(es.Memory[i].FullOffloaded, "Yes", "No")),
						sprintf(es.Memory[i].RAM.UMA),
						sprintf(es.Memory[i].RAM.NonUMA),
					}
					for _, v := range es.Memory[i].VRAMs {
						bds[i] = append(bds[i],
							sprintf(v.UMA),
							sprintf(v.NonUMA))
					}
				}
			} else {
				hds = [][]any{
					{
						"Arch",
						"Offload Layers",
						"Full Offloaded",
						"RAM",
						"RAM",
					},
					{
						"Arch",
						"Offload Layers",
						"Full Offloaded",
						"UMA",
						"NonUMA",
					},
				}
				for i := range es.Memory[0].VRAMs {
					hds[0] = append(hds[0], fmt.Sprintf("VRAM %d", i), fmt.Sprintf("VRAM %d", i))
					hds[1] = append(hds[1], "UMA", "NonUMA")
				}

				bds = [][]any{
					{
						sprintf(es.Architecture),
						sprintf(es.Memory[0].OffloadLayers),
						sprintf(tenary(es.Memory[0].FullOffloaded, "Yes", "No")),
						sprintf(es.Memory[0].RAM.UMA),
						sprintf(es.Memory[0].RAM.NonUMA),
					},
				}
				for _, v := range es.Memory[0].VRAMs {
					bds[0] = append(bds[0],
						sprintf(v.UMA),
						sprintf(v.NonUMA))
				}
			}
			tfprint(c.OutOrStdout(), true, hds, bds)

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
	c.Flags().StringVar(&splitMode, "split-mode", splitMode, "Specify the split mode, such as layer, row, none.")
	c.Flags().StringVar(&tensorSplit, "tensor-split", tensorSplit, "Specify the tensor split fraction.")
	c.Flags().UintVar(&mainGPU, "main-gpu", mainGPU, "Specify the main GPU index.")
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
