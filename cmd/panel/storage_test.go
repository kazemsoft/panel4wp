package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kazemsoft/panel4wp/internal/core"
)

func TestDiskCapabilityNeverClaimsAnAssignedQuota(t *testing.T) {
	for _, capability := range []*core.StorageCapability{
		nil,
		{Reason: "filesystem", WordPressFilesystem: "ext4", DatabaseFilesystem: "ext4"},
		{Reason: "supported", WordPressFilesystem: "xfs", DatabaseFilesystem: "xfs", SameFilesystem: true, ProjectAccounting: true, ProjectEnforcement: true},
	} {
		var output bytes.Buffer
		if err := diskQuotaCapability(view{Language: "en", DiskCapability: capability}).Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		html := output.String()
		if !strings.Contains(html, "Not enabled") || !strings.Contains(html, "No disk limit is currently enforced") || !strings.Contains(html, "<span>Storage guide</span>") || strings.Contains(html, "<form") {
			t.Fatal("filesystem capability presented as active quota", html)
		}
		if capability != nil && capability.SupportsProjectQuotas() && !strings.Contains(html, "No quota has been assigned") {
			t.Fatal("support not distinguished from assignment")
		}
	}
}
