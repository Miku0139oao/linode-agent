package ops

import (
	"context"
	"fmt"

	"github.com/Miku0139oao/linode-agent/internal/api"
)

// AccountTransfer returns this month's pooled network transfer usage.
func (s *Service) AccountTransfer(ctx context.Context) (Object, api.Meta, error) {
	obj, meta, err := s.get(ctx, "/account/transfer", ListOpts{})
	return obj, meta, err
}

// InstanceTransfer returns one instance's network transfer record.
//
// Known API quirk (verified 2026-10): /linode/instances/{id}/transfer reports
// `used` in BYTES while `quota` and `billable` are in GB, even though the
// reference documents all three as GB. /account/transfer, by contrast, is in GB
// for every field. Use TransferUsed for the instance endpoint.
func (s *Service) InstanceTransfer(ctx context.Context, id int) (Object, api.Meta, error) {
	obj, meta, err := s.get(ctx, fmt.Sprintf("/linode/instances/%d/transfer", id), ListOpts{})
	return obj, meta, err
}

// num coerces a JSON number to float64.
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

// BytesToGB converts a byte count to decimal GB (10^9 bytes).
func BytesToGB(v any) float64 { return num(v) / 1e9 }

// UsageRow is one instance's row in the network usage report.
type UsageRow struct {
	ID         int     `json:"id"`
	Label      string  `json:"label"`
	Region     string  `json:"region"`
	UsedGB     float64 `json:"used_gb"`
	QuotaGB    float64 `json:"quota_gb"`
	BillableGB float64 `json:"billable_gb"`
}

// UsageReport combines the account pool with per-instance numbers.
type UsageReport struct {
	PoolUsedGB     float64    `json:"pool_used_gb"`
	PoolQuotaGB    float64    `json:"pool_quota_gb"`
	PoolBillableGB float64    `json:"pool_billable_gb"`
	Percent        float64    `json:"percent"`
	Instances      []UsageRow `json:"instances"`
}

// NetworkUsage builds the network usage report for the whole account.
func (s *Service) NetworkUsage(ctx context.Context) (*UsageReport, error) {
	acct, _, err := s.AccountTransfer(ctx)
	if err != nil {
		return nil, err
	}
	rep := &UsageReport{
		// /account/transfer reports every field in GB.
		PoolUsedGB:     num(acct["used"]),
		PoolQuotaGB:    num(acct["quota"]),
		PoolBillableGB: num(acct["billable"]),
	}
	if rep.PoolQuotaGB > 0 {
		rep.Percent = rep.PoolUsedGB / rep.PoolQuotaGB * 100
	}

	linodes, _, err := s.ListLinodes(ctx, ListOpts{Limit: 200})
	if err != nil {
		return rep, err
	}
	for _, ln := range linodes {
		id := 0
		if v, ok := ln["id"].(float64); ok {
			id = int(v)
		}
		if id == 0 {
			continue
		}
		tr, _, terr := s.InstanceTransfer(ctx, id)
		if terr != nil {
			// Keep going: one failure shouldn't blank the whole report.
			continue
		}
		rep.Instances = append(rep.Instances, UsageRow{
			ID:         id,
			Label:      asString(ln["label"]),
			Region:     asString(ln["region"]),
			UsedGB:     BytesToGB(tr["used"]), // instance endpoint is in bytes
			QuotaGB:    num(tr["quota"]),
			BillableGB: num(tr["billable"]),
		})
	}
	return rep, nil
}
