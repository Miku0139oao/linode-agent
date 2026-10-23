package cli

import (
	"fmt"

	"github.com/Miku0139oao/linode-agent/internal/render"
	"github.com/spf13/cobra"
)

func newNetworkCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "網路相關資訊",
	}

	usage := &cobra.Command{
		Use:   "usage",
		Short: "本月網路用量（帳號池 + 逐台）",
		Long: "顯示帳號的每月網路傳輸配額與用量。\n" +
			"註：/linode/instances/{id}/transfer 回傳的是 bytes（文件寫 GB），這裡已換算成 GB。",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rep, err := svc.NetworkUsage(ctx)
			if err != nil {
				return err
			}
			if o.JSON {
				return render.JSON(o.Stdout, rep)
			}

			rows := make([]map[string]any, 0, len(rep.Instances))
			for _, r := range rep.Instances {
				rows = append(rows, map[string]any{
					"label":       r.Label,
					"region":      r.Region,
					"used_gb":     fmt.Sprintf("%.2f", r.UsedGB),
					"quota_gb":    fmt.Sprintf("%.0f", r.QuotaGB),
					"billable_gb": fmt.Sprintf("%.2f", r.BillableGB),
				})
			}
			fields := []string{"label", "region", "used_gb", "quota_gb", "billable_gb"}
			if err := render.Table(o.Stdout, rows, fields); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "\n帳號池：%.2f / %.0f GB（%.2f%%），計費 %.2f GB\n",
				rep.PoolUsedGB, rep.PoolQuotaGB, rep.Percent, rep.PoolBillableGB)
			return nil
		},
	}

	cmd.AddCommand(usage)
	return cmd
}
