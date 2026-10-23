package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/Miku0139oao/linode-agent/internal/api"
)

// Service holds every high-level operation. CLI, TUI and MCP call into this;
// none of them talk to internal/api directly.
type Service struct {
	API *api.Client
}

// New builds a Service around a client.
func New(c *api.Client) *Service { return &Service{API: c} }

// ListOpts controls list operations.
type ListOpts struct {
	Page     int
	PageSize int
	Limit    int            // 0 = no cap (auto-paginate)
	Filter   map[string]any // becomes X-Filter
	Query    url.Values
}

// Object is a decoded API object.
type Object = map[string]any

func decodeItems(items []json.RawMessage) ([]Object, error) {
	out := make([]Object, 0, len(items))
	for _, raw := range items {
		m, err := api.DecodeMap(raw)
		if err != nil {
			return nil, fmt.Errorf("decode item: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Service) list(ctx context.Context, path string, o ListOpts) ([]Object, api.Meta, error) {
	pageSize := o.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	if o.Limit > 0 && o.PageSize == 0 && o.Page <= 0 {
		items, meta, err := s.API.ListAll(ctx, path, o.Query, o.Filter, pageSize, o.Limit)
		if err != nil {
			return nil, meta, err
		}
		objs, err := decodeItems(items)
		return objs, meta, err
	}
	items, meta, err := s.API.ListPage(ctx, path, o.Query, o.Filter, o.Page, pageSize)
	if err != nil {
		return nil, meta, err
	}
	objs, err := decodeItems(items)
	return objs, meta, err
}

func (s *Service) get(ctx context.Context, path string, o ListOpts) (Object, api.Meta, error) {
	var m Object
	meta, err := s.API.Get(ctx, path, o.Query, o.Filter, &m)
	return m, meta, err
}

// WhoAmI returns the account tied to the token.
func (s *Service) WhoAmI(ctx context.Context) (Object, api.Meta, error) {
	return s.get(ctx, "/account", ListOpts{})
}

// ListLinodes lists Linode instances.
func (s *Service) ListLinodes(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/linode/instances", o)
}

// GetLinode fetches one instance.
func (s *Service) GetLinode(ctx context.Context, id int) (Object, api.Meta, error) {
	return s.get(ctx, fmt.Sprintf("/linode/instances/%d", id), ListOpts{})
}

// ListVolumes lists block storage volumes.
func (s *Service) ListVolumes(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/volumes", o)
}

// ListImages lists disk images.
func (s *Service) ListImages(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/images", o)
}

// ListRegions lists regions.
func (s *Service) ListRegions(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/regions", o)
}

// ListEvents lists account events.
func (s *Service) ListEvents(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/account/events", o)
}

// ListTags lists tags.
func (s *Service) ListTags(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/tags", o)
}

// ListLinodeTypes lists available instance types (used for enum hints).
func (s *Service) ListLinodeTypes(ctx context.Context, o ListOpts) ([]Object, api.Meta, error) {
	return s.list(ctx, "/linode/types", o)
}

// Raw performs an arbitrary request — the escape hatch used by CLI `api` and MCP.
func (s *Service) Raw(ctx context.Context, method, path string, query url.Values, body any) (any, api.Meta, error) {
	data, meta, err := s.API.Send(ctx, method, path, query, nil, body)
	if err != nil {
		return nil, meta, err
	}
	if len(data) == 0 {
		return nil, meta, nil
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, meta, fmt.Errorf("decode response: %w", err)
	}
	return out, meta, nil
}

// WaitForEvent polls an event until it reaches a terminal status.
// Returns the final event object.
func (s *Service) WaitForEvent(ctx context.Context, eventID int) (Object, error) {
	const pollInterval = 2 * time.Second
	for {
		obj, _, err := s.get(ctx, fmt.Sprintf("/account/events/%d", eventID), ListOpts{})
		if err != nil {
			return nil, err
		}
		status, _ := obj["status"].(string)
		switch status {
		case "finished", "failed", "notification":
			return obj, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return obj, ctx.Err()
		case <-timer.C:
		}
	}
}
