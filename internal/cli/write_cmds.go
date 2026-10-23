package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/miku0139/linode-agent/internal/audit"
	"github.com/miku0139/linode-agent/internal/ops"
	"github.com/miku0139/linode-agent/internal/render"
	"github.com/spf13/cobra"
)

// writeFlags are shared by every mutating command. Dry-run is the default;
// --yes is the only way to actually send the request.
type writeFlags struct {
	dryRun bool
	yes    bool
	wait   bool
}

func (w *writeFlags) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&w.dryRun, "dry-run", true, "只顯示將送出的請求，不執行（預設）")
	cmd.Flags().BoolVar(&w.yes, "yes", false, "真的執行（覆寫 --dry-run）")
	cmd.Flags().BoolVar(&w.wait, "wait", false, "執行後等待對應事件完成（最多 180 秒）")
}

// effectiveDryRun resolves the two flags: --yes always wins.
func (w *writeFlags) effectiveDryRun() bool {
	if w.yes {
		return false
	}
	return w.dryRun
}

func printWriteResult(o *Options, res *ops.Result) error {
	if o.JSON {
		return render.JSON(o.Stdout, res)
	}
	if res.DryRun {
		fmt.Fprintf(o.Stdout, "DRY-RUN  %s  %s\n", res.Action, res.Target)
		if len(res.Request) > 0 {
			b, _ := json.Marshal(res.Request)
			fmt.Fprintf(o.Stdout, "  將送出：%s\n", b)
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(o.Stdout, "  注意：%s\n", w)
		}
		fmt.Fprintln(o.Stdout, "  要真的執行請加 --yes")
		return nil
	}
	fmt.Fprintf(o.Stdout, "✓ %s  %s\n", res.Action, res.Target)
	if res.Response != nil {
		return render.JSON(o.Stdout, res.Response)
	}
	return nil
}

// record writes the audit line for a mutating action.
func record(profile string, res *ops.Result, err error) {
	if res == nil {
		return
	}
	e := audit.Entry{
		Profile: profile,
		Action:  res.Action,
		Target:  res.Target,
		DryRun:  res.DryRun,
		OK:      err == nil,
		Request: res.Request,
	}
	if err != nil {
		e.Error = err.Error()
	}
	_ = audit.Write(e)
}

// runWrite wires a mutating command: build → execute (or plan) → audit → print.
// With --wait it also polls the matching event until it finishes.
func runWrite(o *Options, w *writeFlags, fn func(ctx context.Context) (*ops.Result, error)) error {
	svc, cfg, err := service(o)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	res, err := fn(ctx)
	record(cfg.Profile, res, err)
	if err != nil {
		return err
	}
	if err := printWriteResult(o, res); err != nil {
		return err
	}
	if !w.wait || res.DryRun {
		return nil
	}

	fmt.Fprintln(o.Stderr, "等待事件完成…")
	ev, werr := svc.WaitForResult(ctx, res, 180*time.Second)
	if werr != nil {
		return fmt.Errorf("動作已送出，但 %w", werr)
	}
	if o.JSON {
		return render.JSON(o.Stdout, map[string]any{"action": res.Action, "target": res.Target, "event": ev})
	}
	fmt.Fprintf(o.Stdout, "  事件 %v 狀態 %v（%v）\n", ev["id"], ev["status"], ev["action"])
	return nil
}

// newLinodeWriteCmds returns the mutating linode subcommands.
func newLinodeWriteCmds(o *Options) []*cobra.Command {
	var cmds []*cobra.Command

	// create
	var (
		cf        writeFlags
		region    string
		linType   string
		image     string
		label     string
		group     string
		tags      []string
		rootPass  string
		passStdin bool
		booted    bool
	)
	create := &cobra.Command{
		Use:   "create",
		Short: "建立新機器（預設 dry-run）",
		Example: "  linode-agent linodes create --region jp-osa --type g6-nanode-1 \\\n" +
			"      --image linode/ubuntu24.04 --label web2 --root-pass-stdin --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			pass := rootPass
			if passStdin {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("讀取 root password 失敗：%w", err)
				}
				pass = strings.TrimSpace(string(b))
			}
			in := ops.CreateLinodeInput{
				Region: region, Type: linType, Image: image,
				Label: label, Group: group, Tags: tags, RootPass: pass,
			}
			if cmd.Flags().Changed("booted") {
				in.Booted = &booted
			}
			return runWrite(o, &cf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.CreateLinode(ctx, in, cf.effectiveDryRun())
			})
		},
	}
	cf.register(create)
	create.Flags().StringVar(&region, "region", "", "region id（必填，例如 jp-osa）")
	create.Flags().StringVar(&linType, "type", "", "機器規格（必填，例如 g6-nanode-1）")
	create.Flags().StringVar(&image, "image", "", "image id（例如 linode/ubuntu24.04）")
	create.Flags().StringVar(&label, "label", "", "機器名稱")
	create.Flags().StringVar(&group, "group", "", "群組")
	create.Flags().StringSliceVar(&tags, "tags", nil, "tags")
	create.Flags().StringVar(&rootPass, "root-pass", "", "root 密碼（不建議：會留在 shell history）")
	create.Flags().BoolVar(&passStdin, "root-pass-stdin", false, "從 stdin 讀 root 密碼（建議）")
	create.Flags().BoolVar(&booted, "booted", true, "建立後是否開機")
	cmds = append(cmds, create)

	// simple power actions
	power := []struct {
		name, short string
		call        func(s *ops.Service, ctx context.Context, id int, dry bool) (*ops.Result, error)
	}{
		{"boot", "開機", func(s *ops.Service, ctx context.Context, id int, dry bool) (*ops.Result, error) {
			return s.BootLinode(ctx, id, dry)
		}},
		{"shutdown", "關機（graceful）", func(s *ops.Service, ctx context.Context, id int, dry bool) (*ops.Result, error) {
			return s.ShutdownLinode(ctx, id, dry)
		}},
		{"reboot", "重新開機", func(s *ops.Service, ctx context.Context, id int, dry bool) (*ops.Result, error) {
			return s.RebootLinode(ctx, id, dry)
		}},
	}
	for _, p := range power {
		p := p
		var wf writeFlags
		c := &cobra.Command{
			Use:   p.name + " <id>",
			Short: p.short + "（預設 dry-run）",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseInt(args[0], "id")
				if err != nil {
					return err
				}
				return runWrite(o, &wf, func(ctx context.Context) (*ops.Result, error) {
					svc, _, err := service(o)
					if err != nil {
						return nil, err
					}
					return p.call(svc, ctx, id, wf.effectiveDryRun())
				})
			},
		}
		wf.register(c)
		cmds = append(cmds, c)
	}

	// update
	var (
		uw       writeFlags
		newLabel string
		newGroup string
		newTags  []string
	)
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "修改 label / tags / group（預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInt(args[0], "id")
			if err != nil {
				return err
			}
			in := ops.UpdateLinodeInput{}
			if cmd.Flags().Changed("label") {
				in.Label = &newLabel
			}
			if cmd.Flags().Changed("group") {
				in.Group = &newGroup
			}
			if cmd.Flags().Changed("tags") {
				in.Tags = newTags
			}
			return runWrite(o, &uw, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.UpdateLinode(ctx, id, in, uw.effectiveDryRun())
			})
		},
	}
	uw.register(update)
	update.Flags().StringVar(&newLabel, "label", "", "新名稱")
	update.Flags().StringVar(&newGroup, "group", "", "新群組")
	update.Flags().StringSliceVar(&newTags, "tags", nil, "新 tags（覆蓋）")
	cmds = append(cmds, update)

	// resize
	var (
		rf       writeFlags
		newType  string
		autoDisk bool
	)
	resize := &cobra.Command{
		Use:   "resize <id>",
		Short: "變更規格（預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInt(args[0], "id")
			if err != nil {
				return err
			}
			return runWrite(o, &rf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.ResizeLinode(ctx, id, ops.ResizeLinodeInput{Type: newType, AllowAutoDiskResize: autoDisk}, rf.effectiveDryRun())
			})
		},
	}
	rf.register(resize)
	resize.Flags().StringVar(&newType, "type", "", "目標規格（必填）")
	resize.Flags().BoolVar(&autoDisk, "allow-auto-disk-resize", false, "允許自動擴充磁碟")
	cmds = append(cmds, resize)

	// delete
	var df writeFlags
	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "刪除機器（不可逆，預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInt(args[0], "id")
			if err != nil {
				return err
			}
			return runWrite(o, &df, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.DeleteLinode(ctx, id, df.effectiveDryRun())
			})
		},
	}
	df.register(del)
	cmds = append(cmds, del)

	// disks (read helper, needed for image capture)
	disks := &cobra.Command{
		Use:   "disks <id>",
		Short: "列出機器的磁碟（image capture 需要 disk_id）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInt(args[0], "id")
			if err != nil {
				return err
			}
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListDisks(ctx, id)
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"id", "label", "size", "filesystem", "status"})
		},
	}
	cmds = append(cmds, disks)

	return cmds
}

// newVolumeWriteCmds returns the mutating volume subcommands.
func newVolumeWriteCmds(o *Options) []*cobra.Command {
	var cmds []*cobra.Command

	var (
		cf     writeFlags
		label  string
		size   int
		region string
		linode int
		tags   []string
	)
	create := &cobra.Command{
		Use:   "create",
		Short: "建立 volume（預設 dry-run）",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := ops.CreateVolumeInput{Label: label, Size: size, Region: region, LinodeID: linode, Tags: tags}
			return runWrite(o, &cf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.CreateVolume(ctx, in, cf.effectiveDryRun())
			})
		},
	}
	cf.register(create)
	create.Flags().StringVar(&label, "label", "", "volume 名稱（必填）")
	create.Flags().IntVar(&size, "size", 0, "大小 GB（必填）")
	create.Flags().StringVar(&region, "region", "", "region（與 --linode 二選一）")
	create.Flags().IntVar(&linode, "linode", 0, "直接掛到某台機器")
	create.Flags().StringSliceVar(&tags, "tags", nil, "tags")
	cmds = append(cmds, create)

	var (
		af       writeFlags
		attachTo int
	)
	attach := &cobra.Command{
		Use:   "attach <volume-id>",
		Short: "掛載 volume 到機器（預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vid, err := parseInt(args[0], "volume-id")
			if err != nil {
				return err
			}
			return runWrite(o, &af, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.AttachVolume(ctx, vid, attachTo, af.effectiveDryRun())
			})
		},
	}
	af.register(attach)
	attach.Flags().IntVar(&attachTo, "linode", 0, "目標機器 id（必填）")
	cmds = append(cmds, attach)

	var tf writeFlags
	detach := &cobra.Command{
		Use:   "detach <volume-id>",
		Short: "卸載 volume（預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vid, err := parseInt(args[0], "volume-id")
			if err != nil {
				return err
			}
			return runWrite(o, &tf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.DetachVolume(ctx, vid, tf.effectiveDryRun())
			})
		},
	}
	tf.register(detach)
	cmds = append(cmds, detach)

	var (
		zf    writeFlags
		newSz int
	)
	resize := &cobra.Command{
		Use:   "resize <volume-id>",
		Short: "擴充 volume（只能變大，預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vid, err := parseInt(args[0], "volume-id")
			if err != nil {
				return err
			}
			return runWrite(o, &zf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.ResizeVolume(ctx, vid, newSz, zf.effectiveDryRun())
			})
		},
	}
	zf.register(resize)
	resize.Flags().IntVar(&newSz, "size", 0, "新大小 GB（必填）")
	cmds = append(cmds, resize)

	var vf writeFlags
	del := &cobra.Command{
		Use:   "delete <volume-id>",
		Short: "刪除 volume（不可逆，預設 dry-run）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vid, err := parseInt(args[0], "volume-id")
			if err != nil {
				return err
			}
			return runWrite(o, &vf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.DeleteVolume(ctx, vid, vf.effectiveDryRun())
			})
		},
	}
	vf.register(del)
	cmds = append(cmds, del)

	return cmds
}

// newImageWriteCmds returns the mutating image subcommands.
func newImageWriteCmds(o *Options) []*cobra.Command {
	var (
		cf          writeFlags
		diskID      int
		label       string
		description string
		tags        []string
	)
	capture := &cobra.Command{
		Use:   "capture",
		Short: "從磁碟建立 image（預設 dry-run）",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := ops.CaptureImageInput{DiskID: diskID, Label: label, Description: description, Tags: tags}
			return runWrite(o, &cf, func(ctx context.Context) (*ops.Result, error) {
				svc, _, err := service(o)
				if err != nil {
					return nil, err
				}
				return svc.CaptureImage(ctx, in, cf.effectiveDryRun())
			})
		},
	}
	cf.register(capture)
	capture.Flags().IntVar(&diskID, "disk", 0, "來源磁碟 id（必填，用 linodes disks 查）")
	capture.Flags().StringVar(&label, "label", "", "image 名稱（必填）")
	capture.Flags().StringVar(&description, "description", "", "描述")
	capture.Flags().StringSliceVar(&tags, "tags", nil, "tags")
	return []*cobra.Command{capture}
}
