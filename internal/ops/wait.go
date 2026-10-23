package ops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// waitTarget maps a Result (verb + target) onto the Linode event that reports
// its progress. ok=false means this action has no pollable event.
func waitTarget(verb, target string) (entityID int, action string, ok bool) {
	parts := strings.SplitN(target, "/", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	id, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, "", false
	}
	switch verb {
	case "linode_boot", "linode_shutdown", "linode_reboot", "linode_resize", "linode_delete",
		"volume_create", "volume_attach", "volume_detach", "volume_resize", "volume_delete":
		return id, verb, true
	}
	// linode_update has no event; image_capture reports on the image, not the disk.
	return 0, "", false
}

// WaitForResult polls /account/events until the event produced by this action
// reaches a terminal status, or the timeout expires.
func (s *Service) WaitForResult(ctx context.Context, res *Result, timeout time.Duration) (Object, error) {
	if res == nil {
		return nil, fmt.Errorf("nothing to wait for")
	}
	entityID, action, ok := waitTarget(res.Action, res.Target)
	if !ok {
		return nil, fmt.Errorf("%s 沒有可等待的事件", res.Action)
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	start := time.Now().UTC().Add(-2 * time.Second)
	deadline := time.Now().Add(timeout)

	for {
		rows, _, err := s.list(ctx, "/account/events", ListOpts{
			Page:     1,
			PageSize: 25,
			Filter:   map[string]any{"entity.id": entityID},
		})
		if err != nil {
			return nil, err
		}
		for _, ev := range rows {
			if asString(ev["action"]) != action {
				continue
			}
			if t, ok := parseEventTime(ev["created"]); ok && t.Before(start) {
				continue
			}
			switch asString(ev["status"]) {
			case "finished", "notification", "failed":
				if asString(ev["status"]) == "failed" {
					return ev, fmt.Errorf("%s 失敗", action)
				}
				return ev, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待 %s 超時（%s）", action, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// parseEventTime reads the API's "2006-01-02T15:04:05" (UTC, no zone suffix).
func parseEventTime(v any) (time.Time, bool) {
	s := asString(v)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
