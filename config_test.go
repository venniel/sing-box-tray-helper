package main

import (
	"strings"
	"testing"
)

const sampleConfig = `{
  // mixed inbound used for the system proxy
  "inbounds": [
    {
      "type": "mixed",
      "tag": "mixed-in",
      "listen": "127.0.0.1",
      "listen_port": 1080,
    },
    {
      "type": "tun",
      "tag": "tun-in",
      "address": ["172.19.0.1/30"],
    },
  ],
  "outbounds": [
    {
      "type": "selector",
      "tag": "proxy-select",
      "outbounds": ["Proxy1", "Proxy2"],
      "default": "Proxy2",
    },
    {"type": "direct", "tag": "direct"},
  ],
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": "s3cret",
    }
  }
}`

func TestReadSingBoxConfig(t *testing.T) {
	cfg, err := readSingBoxConfig(sampleConfig)
	if err != nil {
		t.Fatalf("readSingBoxConfig: %v", err)
	}

	if cfg.ProxyHost != "127.0.0.1" || cfg.ProxyPort != 1080 {
		t.Errorf("mixed inbound = %q:%d", cfg.ProxyHost, cfg.ProxyPort)
	}
	if !cfg.HasTunInbound {
		t.Error("tun inbound was not detected")
	}
	if len(cfg.Selectors) != 1 {
		t.Fatalf("selectors = %d, want 1", len(cfg.Selectors))
	}
	sel := cfg.Selectors[0]
	if sel.Name != "proxy-select" || len(sel.Outbounds) != 2 || sel.DefaultIndex != 1 || sel.DefaultName != "Proxy2" {
		t.Errorf("selector parsed incorrectly: %+v", sel)
	}
	if !cfg.ClashAPI.IsConfigured() || cfg.ClashAPI.ExternalController != "127.0.0.1:9090" || cfg.ClashAPI.Secret != "s3cret" {
		t.Errorf("clash api parsed incorrectly: %+v", cfg.ClashAPI)
	}
	if err := checkSingBoxConfig(cfg); err != nil {
		t.Errorf("checkSingBoxConfig: %v", err)
	}

	if !strings.Contains(cfg.JSONWithTun, `"tun"`) {
		t.Error("JSONWithTun should contain the tun inbound")
	}
	if strings.Contains(cfg.JSONWithoutTun, `"tun"`) {
		t.Error("JSONWithoutTun should not contain the tun inbound")
	}
	if !strings.Contains(cfg.JSONWithoutTun, `"mixed"`) {
		t.Error("JSONWithoutTun should keep other inbounds")
	}
	if strings.Contains(cfg.JSONWithTun, "//") {
		t.Error("comments should be stripped from output JSON")
	}
}

func TestReadSingBoxConfigCreatesClashAPI(t *testing.T) {
	jsonText := `{
	  "inbounds": [{"type": "mixed", "listen": "127.0.0.1", "listen_port": 7890}],
	  "outbounds": [{"type": "selector", "tag": "sel", "outbounds": ["A", "B"]}]
	}`

	cfg, err := readSingBoxConfig(jsonText)
	if err != nil {
		t.Fatalf("readSingBoxConfig: %v", err)
	}
	if !cfg.ClashAPI.IsConfigured() {
		t.Error("clash_api should be created when selectors exist")
	}
	if cfg.ClashAPI.ExternalController != "127.0.0.1:9090" {
		t.Errorf("external controller = %q", cfg.ClashAPI.ExternalController)
	}
	if cfg.ClashAPI.Secret == "" {
		t.Error("generated secret should not be empty")
	}
	if !strings.Contains(cfg.JSONWithTun, `"clash_api"`) {
		t.Error("generated clash_api should be written back to the config")
	}
	if cfg.Selectors[0].DefaultIndex != -1 {
		t.Errorf("DefaultIndex = %d, want -1 without a default", cfg.Selectors[0].DefaultIndex)
	}
}

func TestReadSingBoxConfigErrors(t *testing.T) {
	if _, err := readSingBoxConfig("{broken"); err == nil {
		t.Error("expected an error for invalid JSON")
	}
	if _, err := readSingBoxConfig(`[1,2,3]`); err == nil {
		t.Error("expected an error for a non-object root")
	}

	cfg, err := readSingBoxConfig(`{"inbounds": [{"type": "tun"}]}`)
	if err != nil {
		t.Fatalf("readSingBoxConfig: %v", err)
	}
	if err := checkSingBoxConfig(cfg); err == nil {
		t.Error("expected an error when there is no mixed inbound")
	}
}

func TestReadSingBoxConfigCaseInsensitiveTypes(t *testing.T) {
	cfg, err := readSingBoxConfig(`{
	  "inbounds": [{"type": "MIXED", "listen": "0.0.0.0", "listen_port": 1081}, {"type": "Tun"}],
	  "outbounds": [{"type": "SELECTOR", "tag": "S", "outbounds": ["A"]}]
	}`)
	if err != nil {
		t.Fatalf("readSingBoxConfig: %v", err)
	}
	if cfg.ProxyPort != 1081 || !cfg.HasTunInbound || len(cfg.Selectors) != 1 {
		t.Errorf("case-insensitive matching failed: %+v", cfg)
	}
}

func TestNormalizeJSON(t *testing.T) {
	input := `{
  // line comment
  "a": "http://example.com//x", /* block
  comment */
  "b": [1, 2,],
  "c": {"d": "quote\" inside",},
}`
	want := `{"a":"http://example.com//x","b":[1,2],"c":{"d":"quote\" inside"}}`
	if got := normalizeJSON(input); got != want {
		t.Errorf("normalizeJSON = %q, want %q", got, want)
	}
}

func TestNormalizeJSONEmptyContainers(t *testing.T) {
	input := "{\r\n\t\"a\": [ ],\r\n\t\"b\": { },\r\n}"
	want := `{"a":[],"b":{}}`
	if got := normalizeJSON(input); got != want {
		t.Errorf("normalizeJSON = %q, want %q", got, want)
	}
}

func TestClashAPIDashboardURL(t *testing.T) {
	online := "https://venniel.github.io/Yacd-meta"

	if got := (clashAPIConfig{}).DashboardURL(); got != online {
		t.Errorf("DashboardURL without external_ui = %q, want %q", got, online)
	}

	cfg := clashAPIConfig{ExternalController: "127.0.0.1:9090", ExternalUI: "ui"}
	if got, want := cfg.DashboardURL(), "http://127.0.0.1:9090/ui/"; got != want {
		t.Errorf("DashboardURL = %q, want %q", got, want)
	}

	cfg.ExternalController = "0.0.0.0:9090"
	if got, want := cfg.DashboardURL(), "http://127.0.0.1:9090/ui/"; got != want {
		t.Errorf("DashboardURL = %q, want %q", got, want)
	}

	cfg.ExternalController = "0.0.0.0"
	if got, want := cfg.DashboardURL(), "http://127.0.0.1/ui/"; got != want {
		t.Errorf("DashboardURL = %q, want %q", got, want)
	}

	cfg = clashAPIConfig{ExternalController: "127.0.0.1:9090", ExternalUI: "ui", Secret: "a b&c"}
	if got, want := cfg.DashboardURL(), "http://127.0.0.1:9090/ui/?secret=a+b%26c"; got != want {
		t.Errorf("DashboardURL = %q, want %q", got, want)
	}

	cfg = clashAPIConfig{ExternalController: "127.0.0.1:9090", ExternalUI: " https://example.com/ui/ "}
	if got, want := cfg.DashboardURL(), "https://example.com/ui/"; got != want {
		t.Errorf("DashboardURL with absolute external_ui = %q, want %q", got, want)
	}

	cfg = clashAPIConfig{ExternalUI: "ui"}
	if got := cfg.DashboardURL(); got != online {
		t.Errorf("DashboardURL with external_ui but no controller = %q, want %q", got, online)
	}
}
