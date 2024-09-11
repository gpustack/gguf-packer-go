package main

import (
	"errors"
	"fmt"
	"net"
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
		rpcServers         string
		platformFootprint  = "150,250"
		noMMap             bool
		offloadLayers      = -1
		offloadLayersDraft = -1
		offloadLayersStep  uint64
		deviceMetrics      []string
		inShort            bool
		inJson             bool
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
				eopts []ggufparser.LLaMACppRunEstimateOption

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
					eopts = append(eopts, ggufparser.WithContextSize(int32(v)))
				case "-b", "--batch-size":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					eopts = append(eopts, ggufparser.WithLogicalBatchSize(int32(v)))
				case "-ub", "--ubatch-size":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					eopts = append(eopts, ggufparser.WithPhysicalBatchSize(int32(v)))
				case "-np", "--parallel":
					if i+1 >= s {
						continue
					}
					i++
					v, err := strconv.ParseInt(cmd[i], 10, 64)
					if err != nil {
						continue
					}
					eopts = append(eopts, ggufparser.WithParallelSize(int32(v)))
				case "-nkvo", "--no-kv-offload":
					eopts = append(eopts, ggufparser.WithoutOffloadKVCache())
				case "-ctk", "--cache-type-k":
					if i+1 >= s {
						continue
					}
					i++
					eopts = append(eopts, ggufparser.WithCacheKeyType(toGGMLType(cmd[i])))
				case "-ctv", "--cache-type-v":
					if i+1 >= s {
						continue
					}
					i++
					eopts = append(eopts, ggufparser.WithCacheValueType(toGGMLType(cmd[i])))
				case "-fa", "--flash-attn":
					eopts = append(eopts, ggufparser.WithFlashAttention())
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
				eopts = append(eopts, ggufparser.WithContextSize(int32(ctxSize)))
			}
			if logicalBatchSize > 0 {
				eopts = append(eopts, ggufparser.WithLogicalBatchSize(int32(logicalBatchSize)))
			}
			if physicalBatchSize > 0 {
				if physicalBatchSize > logicalBatchSize {
					return errors.New("--ubatch-size must be less than or equal to --batch-size")
				}
				eopts = append(eopts, ggufparser.WithPhysicalBatchSize(int32(physicalBatchSize)))
			}
			if parallelSize > 0 {
				eopts = append(eopts, ggufparser.WithParallelSize(int32(parallelSize)))
			}
			if cacheKeyType != "" {
				eopts = append(eopts, ggufparser.WithCacheKeyType(toGGMLType(cacheKeyType)))
			}
			if cacheValueType != "" {
				eopts = append(eopts, ggufparser.WithCacheValueType(toGGMLType(cacheValueType)))
			}
			if noKVOffload {
				eopts = append(eopts, ggufparser.WithoutOffloadKVCache())
			}
			if flashAttention {
				eopts = append(eopts, ggufparser.WithFlashAttention())
			}
			switch splitMode {
			case "row":
				eopts = append(eopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeRow))
			case "none":
				eopts = append(eopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeNone))
			default:
				eopts = append(eopts, ggufparser.WithSplitMode(ggufparser.LLaMACppSplitModeLayer))
			}
			if tensorSplit != "" {
				tss := strings.Split(tensorSplit, ",")
				var vs float64
				vv := make([]float64, len(tss))
				vf := make([]float64, len(tss))
				for i, s := range tss {
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
				eopts = append(eopts, ggufparser.WithTensorSplitFraction(vf))
				if mainGPU < uint(len(vv)) {
					eopts = append(eopts, ggufparser.WithMainGPUIndex(int(mainGPU)))
				} else {
					return errors.New("--main-gpu must be less than item size of --tensor-split")
				}
				if rpcServers != "" {
					rss := strings.Split(rpcServers, ",")
					if len(rss) > len(tss) {
						return errors.New("--rpc has more items than --tensor-split")
					}
					rpc := make([]string, len(rss))
					for i, s := range rss {
						s = strings.TrimSpace(s)
						if _, _, err := net.SplitHostPort(s); err != nil {
							return errors.New("--rpc has invalid host:port")
						}
						rpc[i] = s
					}
					eopts = append(eopts, ggufparser.WithRPCServers(rpc))
				}
			}
			if dmss := deviceMetrics; len(dmss) > 0 {
				dms := make([]ggufparser.LLaMACppRunDeviceMetric, len(dmss))
				for i := range dmss {
					ss := strings.Split(dmss[i], ";")
					if len(ss) < 2 {
						return errors.New("--device-metric has invalid format")
					}
					dms[i].FLOPS, err = ggufparser.ParseFLOPSScalar(strings.TrimSpace(ss[0]))
					if err != nil {
						return fmt.Errorf("--device-metric has invalid FLOPS: %w", err)
					}
					dms[i].UpBandwidth, err = ggufparser.ParseBytesPerSecondScalar(strings.TrimSpace(ss[1]))
					if err != nil {
						return fmt.Errorf("--device-metric has invalid Up Bandwidth: %w", err)
					}
					if len(ss) > 2 {
						dms[i].DownBandwidth, err = ggufparser.ParseBytesPerSecondScalar(strings.TrimSpace(ss[2]))
						if err != nil {
							return fmt.Errorf("--device-metric has invalid Down Bandwidth: %w", err)
						}
					} else {
						dms[i].DownBandwidth = dms[i].UpBandwidth
					}
				}
				eopts = append(eopts, ggufparser.WithDeviceMetrics(dms))
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
			if d := cf.Config.Drafter; d != nil {
				dopts := eopts[:len(eopts):len(eopts)]
				if offloadLayersDraft >= 0 {
					dopts = append(dopts, ggufparser.WithOffloadLayers(uint64(offloadLayersDraft)))
				}
				de := d.EstimateLLaMACppRun(dopts...)
				eopts = append(eopts, ggufparser.WithDrafter(&de))
			}
			if p := cf.Config.Projector; p != nil {
				popts := eopts[:len(eopts):len(eopts)]
				pe := p.EstimateLLaMACppRun(popts...)
				eopts = append(eopts, ggufparser.WithProjector(&pe))
			}
			if len(cf.Config.Adapters) > 0 {
				adps := make([]ggufparser.LLaMACppRunEstimate, len(cf.Config.Adapters))
				aopts := eopts[:len(eopts):len(eopts)]
				for i, adpgf := range cf.Config.Adapters {
					ae := adpgf.EstimateLLaMACppRun(aopts...)
					adps[i] = ae
				}
				eopts = append(eopts, ggufparser.WithAdapters(adps))
			}
			if offloadLayers >= 0 {
				eopts = append(eopts, ggufparser.WithOffloadLayers(uint64(offloadLayers)))
			}
			e := cf.Config.Model.EstimateLLaMACppRun(eopts...)

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
				esis := make([]ggufparser.LLaMACppRunEstimateSummaryItem, cnt)
				var wg sync.WaitGroup
				for i := 0; i < cap(esis); i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						eopts := eopts[:len(eopts):len(eopts)]
						eopts = append(eopts, ggufparser.WithOffloadLayers(uint64(i)*offloadLayersStep))
						esis[i] = cf.Config.Model.EstimateLLaMACppRun(eopts...).SummarizeItem(mmap, platformRAM, platformVRAM)
					}(i)
				}
				wg.Wait()
				esis[cap(esis)-1] = es.Items[0]
				es.Items = esis
			}

			w := c.OutOrStdout()
			if inJson {
				jprint(w, es)
				return nil
			}

			var (
				hds [][]any
				bds [][]any
			)
			{
				hds = make([][]any, 2)
				if !inShort {
					hds[0] = []any{
						"Arch",
						"Context Size",
						"Batch Size (L / P)",
						"Flash Attention",
						"MMap Load",
						"Embedding Only",
						"Distributable",
						"Offload Layers",
						"Full Offloaded",
					}
					hds[1] = []any{
						"Arch",
						"Context Size",
						"Batch Size (L / P)",
						"Flash Attention",
						"MMap Load",
						"Embedding Only",
						"Distributable",
						"Offload Layers",
						"Full Offloaded",
					}
				}
				if es.Items[0].MaximumTokensPerSecond != nil {
					hds[0] = append(hds[0], "Max TPS")
					hds[1] = append(hds[1], "Max TPS")
				}
				hds[0] = append(hds[0], "RAM", "RAM", "RAM")
				hds[1] = append(hds[1], "Layers (I + T + O)", "UMA", "NonUMA")
				for _, v := range es.Items[0].VRAMs {
					var hd string
					if v.Remote {
						hd = fmt.Sprintf("RPC %d (V)RAM", v.Position)
					} else {
						hd = fmt.Sprintf("VRAM %d", v.Position)
					}
					hds[0] = append(hds[0], hd, hd, hd)
					hds[1] = append(hds[1], "Layers (T + O)", "UMA", "NonUMA")
				}

				bds = make([][]any, len(es.Items))
				for i := range es.Items {
					if !inShort {
						bds[i] = []any{
							sprintf(es.Architecture),
							sprintf(es.ContextSize),
							sprintf("%d / %d", es.LogicalBatchSize, es.PhysicalBatchSize),
							sprintf(tenary(flashAttention, tenary(es.FlashAttention, "Enabled", "Unsupported"), "Disabled")),
							sprintf(tenary(mmap, tenary(!es.NoMMap, "Enabled", "Unsupported"), "Disabled")),
							sprintf(tenary(es.EmbeddingOnly, "Yes", "No")),
							sprintf(tenary(es.Distributable, "Supported", "Unsupported")),
							sprintf(tenary(es.Items[i].FullOffloaded, sprintf("%d (%d + 1)",
								es.Items[i].OffloadLayers, es.Items[i].OffloadLayers-1), es.Items[i].OffloadLayers)),
							sprintf(tenary(es.Items[i].FullOffloaded, "Yes", "No")),
						}
					}
					if es.Items[i].MaximumTokensPerSecond != nil {
						bds[i] = append(bds[i],
							sprintf(*es.Items[i].MaximumTokensPerSecond))
					}
					bds[i] = append(bds[i],
						sprintf("1 + %d + %d", es.Items[i].RAM.HandleLayers, tenary(es.Items[i].RAM.HandleOutputLayer, 1, 0)),
						sprintf(es.Items[i].RAM.UMA),
						sprintf(es.Items[i].RAM.NonUMA))
					for _, v := range es.Items[i].VRAMs {
						bds[i] = append(bds[i],
							sprintf("%d + %d", v.HandleLayers, tenary(v.HandleOutputLayer, 1, 0)),
							sprintf(v.UMA),
							sprintf(v.NonUMA))
					}
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
	c.Flags().StringVar(&rpcServers, "rpc", rpcServers, "Specify the RPC servers.")
	c.Flags().StringVar(&platformFootprint, "platform-footprint", platformFootprint, "Specify the platform footprint(RAM,VRAM) in MiB.")
	c.Flags().BoolVar(&noMMap, "no-mmap", noMMap, "Disable the memory mapping.")
	c.Flags().IntVar(&offloadLayers, "gpu-layers", offloadLayers, "Specify the offload layers.")
	c.Flags().IntVar(&offloadLayersDraft, "gpu-layers-draft", offloadLayersDraft, "Specify the offload layers draft.")
	c.Flags().Uint64Var(&offloadLayersStep, "gpu-layers-step", offloadLayersStep, "Specify the offload layers step.")
	c.Flags().StringSliceVar(&deviceMetrics, "device-metric", deviceMetrics, "Specify the device metric, in form of \"FLOPS;Up Bandwidth[;Down Bandwidth]\". "+
		"The FLOPS unit, select from [PFLOPS, TFLOPS, GFLOPS, MFLOPS, KFLOPS]. "+
		"The Up/Down Bandwidth unit, select from [PiBps, TiBps, GiBps, MiBps, KiBps, PBps, TBps, GBps, MBps, KBps, Pbps, Tbps, Gbps, Mbps, Kbps].")
	c.Flags().BoolVar(&inShort, "in-short", inShort, "Output as short format.")
	c.Flags().BoolVar(&inJson, "json", inJson, "Output as JSON.")
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
