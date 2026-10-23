package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Miku0139oao/linode-agent/internal/api"
)

// Result describes one mutating action, whether it was applied or only planned.
// DryRun=true means no request was sent to the API.
type Result struct {
	DryRun   bool           `json:"dry_run"`
	Action   string         `json:"action"`
	Target   string         `json:"target"`
	Request  map[string]any `json:"request,omitempty"`
	Response any            `json:"response,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
}

func (s *Service) action(ctx context.Context, verb, target, method, path string, body any, dry bool) (*Result, error) {
	res := &Result{DryRun: dry, Action: verb, Target: target}
	if m, ok := body.(map[string]any); ok && len(m) > 0 {
		res.Request = m
	}
	if dry {
		return res, nil
	}
	data, _, err := s.API.Send(ctx, method, path, nil, nil, body)
	if err != nil {
		return res, err
	}
	if len(data) > 0 {
		var out any
		if err := json.Unmarshal(data, &out); err == nil {
			res.Response = out
		}
	}
	return res, nil
}

// ---- Linode lifecycle ----

// CreateLinodeInput mirrors the writable fields of POST /linode/instances.
type CreateLinodeInput struct {
	Region         string   `json:"region"`
	Type           string   `json:"type"`
	Image          string   `json:"image,omitempty"`
	Label          string   `json:"label,omitempty"`
	RootPass       string   `json:"root_pass,omitempty"`
	AuthorizedKeys []string `json:"authorized_keys,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Group          string   `json:"group,omitempty"`
	Booted         *bool    `json:"booted,omitempty"`
}

// CreateLinode creates an instance.
func (s *Service) CreateLinode(ctx context.Context, in CreateLinodeInput, dry bool) (*Result, error) {
	if in.Region == "" || in.Type == "" {
		return nil, fmt.Errorf("region 與 type 必填")
	}
	body := map[string]any{"region": in.Region, "type": in.Type}
	if in.Image != "" {
		body["image"] = in.Image
	}
	if in.Label != "" {
		body["label"] = in.Label
	}
	if in.RootPass != "" {
		body["root_pass"] = in.RootPass
	}
	if len(in.AuthorizedKeys) > 0 {
		body["authorized_keys"] = in.AuthorizedKeys
	}
	if len(in.Tags) > 0 {
		body["tags"] = in.Tags
	}
	if in.Group != "" {
		body["group"] = in.Group
	}
	if in.Booted != nil {
		body["booted"] = *in.Booted
	}
	res, err := s.action(ctx, "linode_create", "linode", http.MethodPost, "/linode/instances", body, dry)
	if res != nil {
		// Never echo the root password back into logs or MCP output.
		delete(res.Request, "root_pass")
		if in.RootPass != "" {
			res.Warnings = append(res.Warnings, "root_pass 已送出但不會被記錄")
		}
	}
	return res, err
}

// BootLinode powers an instance on.
func (s *Service) BootLinode(ctx context.Context, id int, dry bool) (*Result, error) {
	return s.action(ctx, "linode_boot", linodeTarget(id), http.MethodPost,
		fmt.Sprintf("/linode/instances/%d/boot", id), nil, dry)
}

// ShutdownLinode powers an instance off gracefully.
func (s *Service) ShutdownLinode(ctx context.Context, id int, dry bool) (*Result, error) {
	return s.action(ctx, "linode_shutdown", linodeTarget(id), http.MethodPost,
		fmt.Sprintf("/linode/instances/%d/shutdown", id), nil, dry)
}

// RebootLinode reboots an instance.
func (s *Service) RebootLinode(ctx context.Context, id int, dry bool) (*Result, error) {
	return s.action(ctx, "linode_reboot", linodeTarget(id), http.MethodPost,
		fmt.Sprintf("/linode/instances/%d/reboot", id), nil, dry)
}

// UpdateLinodeInput holds mutable instance metadata.
type UpdateLinodeInput struct {
	Label *string  `json:"label,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Group *string  `json:"group,omitempty"`
}

// UpdateLinode changes label/tags/group.
func (s *Service) UpdateLinode(ctx context.Context, id int, in UpdateLinodeInput, dry bool) (*Result, error) {
	body := map[string]any{}
	if in.Label != nil {
		body["label"] = *in.Label
	}
	if in.Tags != nil {
		body["tags"] = in.Tags
	}
	if in.Group != nil {
		body["group"] = *in.Group
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("沒有要變更的欄位（label / tags / group 至少一個）")
	}
	return s.action(ctx, "linode_update", linodeTarget(id), http.MethodPut,
		fmt.Sprintf("/linode/instances/%d", id), body, dry)
}

// ResizeLinodeInput holds resize parameters.
type ResizeLinodeInput struct {
	Type                string `json:"type"`
	AllowAutoDiskResize bool   `json:"allow_auto_disk_resize"`
}

// ResizeLinode resizes to another type.
func (s *Service) ResizeLinode(ctx context.Context, id int, in ResizeLinodeInput, dry bool) (*Result, error) {
	if in.Type == "" {
		return nil, fmt.Errorf("type 必填（目標規格）")
	}
	body := map[string]any{"type": in.Type}
	if in.AllowAutoDiskResize {
		body["allow_auto_disk_resize"] = true
	}
	res, err := s.action(ctx, "linode_resize", linodeTarget(id), http.MethodPost,
		fmt.Sprintf("/linode/instances/%d/resize", id), body, dry)
	if res != nil {
		res.Warnings = append(res.Warnings, "resize 需要關機中（或會自動關機），完成後記得開機")
	}
	return res, err
}

// DeleteLinode destroys an instance. Callers must gate this behind confirmation.
func (s *Service) DeleteLinode(ctx context.Context, id int, dry bool) (*Result, error) {
	return s.action(ctx, "linode_delete", linodeTarget(id), http.MethodDelete,
		fmt.Sprintf("/linode/instances/%d", id), nil, dry)
}

// ---- Volumes ----

// CreateVolumeInput mirrors POST /volumes.
type CreateVolumeInput struct {
	Label    string   `json:"label"`
	Size     int      `json:"size"`
	Region   string   `json:"region,omitempty"`
	LinodeID int      `json:"linode_id,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// CreateVolume creates a block storage volume.
func (s *Service) CreateVolume(ctx context.Context, in CreateVolumeInput, dry bool) (*Result, error) {
	if in.Label == "" || in.Size <= 0 {
		return nil, fmt.Errorf("label 與 size（GB）必填")
	}
	if in.Region == "" && in.LinodeID == 0 {
		return nil, fmt.Errorf("region 或 linode_id 至少要給一個")
	}
	body := map[string]any{"label": in.Label, "size": in.Size}
	if in.Region != "" {
		body["region"] = in.Region
	}
	if in.LinodeID != 0 {
		body["linode_id"] = in.LinodeID
	}
	if len(in.Tags) > 0 {
		body["tags"] = in.Tags
	}
	return s.action(ctx, "volume_create", "volume", http.MethodPost, "/volumes", body, dry)
}

// AttachVolume attaches a volume to an instance.
func (s *Service) AttachVolume(ctx context.Context, volumeID, linodeID int, dry bool) (*Result, error) {
	if linodeID == 0 {
		return nil, fmt.Errorf("linode_id 必填")
	}
	body := map[string]any{"linode_id": linodeID}
	return s.action(ctx, "volume_attach", fmt.Sprintf("volume/%d", volumeID), http.MethodPost,
		fmt.Sprintf("/volumes/%d/attach", volumeID), body, dry)
}

// DetachVolume detaches a volume.
func (s *Service) DetachVolume(ctx context.Context, volumeID int, dry bool) (*Result, error) {
	return s.action(ctx, "volume_detach", fmt.Sprintf("volume/%d", volumeID), http.MethodPost,
		fmt.Sprintf("/volumes/%d/detach", volumeID), nil, dry)
}

// ResizeVolume grows a volume.
func (s *Service) ResizeVolume(ctx context.Context, volumeID, size int, dry bool) (*Result, error) {
	if size <= 0 {
		return nil, fmt.Errorf("size（GB）必填")
	}
	return s.action(ctx, "volume_resize", fmt.Sprintf("volume/%d", volumeID), http.MethodPost,
		fmt.Sprintf("/volumes/%d/resize", volumeID), map[string]any{"size": size}, dry)
}

// DeleteVolume destroys a volume.
func (s *Service) DeleteVolume(ctx context.Context, volumeID int, dry bool) (*Result, error) {
	return s.action(ctx, "volume_delete", fmt.Sprintf("volume/%d", volumeID), http.MethodDelete,
		fmt.Sprintf("/volumes/%d", volumeID), nil, dry)
}

// ---- Images ----
// CaptureImageInput mirrors POST /images.
type CaptureImageInput struct {
	DiskID      int      `json:"disk_id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// CaptureImage creates an image from a disk.
func (s *Service) CaptureImage(ctx context.Context, in CaptureImageInput, dry bool) (*Result, error) {
	if in.DiskID <= 0 || in.Label == "" {
		return nil, fmt.Errorf("disk_id 與 label 必填")
	}
	body := map[string]any{"disk_id": in.DiskID, "label": in.Label}
	if in.Description != "" {
		body["description"] = in.Description
	}
	if len(in.Tags) > 0 {
		body["tags"] = in.Tags
	}
	return s.action(ctx, "image_capture", fmt.Sprintf("disk/%d", in.DiskID), http.MethodPost, "/images", body, dry)
}

// ListDisks lists an instance's disks (needed for image capture).
func (s *Service) ListDisks(ctx context.Context, linodeID int) ([]Object, api.Meta, error) {
	rows, meta, err := s.list(ctx, fmt.Sprintf("/linode/instances/%d/disks", linodeID), ListOpts{Limit: 100})
	return rows, meta, err
}

func linodeTarget(id int) string { return fmt.Sprintf("linode/%d", id) }
