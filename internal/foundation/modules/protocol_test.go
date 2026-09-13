package modules

import "testing"

func TestParseBaseURLRejectsCredentialsAndPaths(t *testing.T) {
	if _, err := ParseBaseURL("http://user:pass@127.0.0.1:9"); err == nil {
		t.Fatal("userinfo accepted")
	}
	if _, err := ParseBaseURL("http://127.0.0.1:9/prefix"); err == nil {
		t.Fatal("path accepted")
	}
	if _, err := ParseBaseURL("http://127.0.0.1:9?x=1"); err == nil {
		t.Fatal("query accepted")
	}
	got, err := ParseBaseURL("http://127.0.0.1:8091/")
	if err != nil || got != "http://127.0.0.1:8091" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestValidateManifestRejectsIllegalPagesAndCapabilities(t *testing.T) {
	raw := []byte(`{"id":"demo_external","name":"x","version":"1","protocolVersion":1,"capabilities":["lifecycle","ai"],"pages":[]}`)
	if _, err := ValidateExternalManifest(raw, ""); err == nil {
		t.Fatal("unsupported capability accepted")
	}
	raw = []byte(`{"id":"demo_external","name":"x","version":"1","protocolVersion":2,"capabilities":["lifecycle"]}`)
	if _, err := ValidateExternalManifest(raw, ""); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
	raw = []byte(`{"id":"demo_external","name":"x","version":"1","protocolVersion":1,"capabilities":["lifecycle","pages"],"pages":[{"key":"other.overview","label":"x","entry":"/ui/index.html","order":1}]}`)
	if _, err := ValidateExternalManifest(raw, ""); err == nil {
		t.Fatal("foreign page key accepted")
	}
	raw = []byte(`{"id":"demo_external","name":"x","version":"1","protocolVersion":1,"capabilities":["lifecycle","pages"],"pages":[{"key":"demo_external.overview","label":"x","entry":"/_workbench/manifest","order":1}]}`)
	if _, err := ValidateExternalManifest(raw, ""); err == nil {
		t.Fatal("management entry accepted")
	}
	raw = []byte(`{"id":"demo_external","name":"x","version":"1","protocolVersion":1,"capabilities":["lifecycle","pages"],"pages":[{"key":"demo_external.overview","label":"x","entry":"/ui/index.html","order":1}]}`)
	if _, err := ValidateExternalManifest(raw, ""); err != nil {
		t.Fatal(err)
	}
}
