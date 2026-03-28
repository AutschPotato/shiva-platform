package handler

import (
	"strings"
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestValidateTemplateRequestCoercesLegacyUploadBuilderPayloads(t *testing.T) {
	req := &model.TestTemplateRequest{
		Name:          "from-result",
		Mode:          "upload",
		URL:           "http://target-lb:8090/health",
		ScriptContent: "export default function(){}",
		HTTPMethod:    "post",
		ContentType:   "application/json",
		PayloadJSON:   `{"hello":"world"}`,
	}

	if msg := validateTemplateRequest(req); msg != "" {
		t.Fatalf("expected request to validate, got %q", msg)
	}
	if req.Mode != "builder" {
		t.Fatalf("expected mode to be coerced to builder, got %q", req.Mode)
	}
	if req.HTTPMethod != "POST" {
		t.Fatalf("expected normalized HTTP method POST, got %q", req.HTTPMethod)
	}
}

func TestNormalizeTemplateRequestPayloadRejectsUploadPayloadOverrides(t *testing.T) {
	req := &model.TestTemplateRequest{
		Name:          "upload-template",
		Mode:          "upload",
		ScriptContent: "export default function(){}",
		HTTPMethod:    "POST",
		ContentType:   "application/json",
		PayloadJSON:   `{"x":1}`,
	}

	err := normalizeTemplateRequestPayload(req)
	if err == nil {
		t.Fatalf("expected upload template payload override to be rejected")
	}
	if !strings.Contains(err.Error(), "payload settings are only supported for builder-mode tests") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferTemplateModeDefaults(t *testing.T) {
	tests := []struct {
		name string
		req  *model.TestTemplateRequest
		want string
	}{
		{
			name: "script only defaults to upload",
			req: &model.TestTemplateRequest{
				Name:          "script-only",
				ScriptContent: "export default function(){}",
			},
			want: "upload",
		},
		{
			name: "builder hints default to builder",
			req: &model.TestTemplateRequest{
				Name:       "builder-hints",
				URL:        "http://target-lb:8090/health",
				HTTPMethod: "GET",
			},
			want: "builder",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := inferTemplateMode(tc.req); got != tc.want {
				t.Fatalf("inferTemplateMode()=%q, want %q", got, tc.want)
			}
		})
	}
}
