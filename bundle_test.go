package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractProfileBundleRestoresManifestAndState(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	manifest := profileBundleManifest{
		Version: bundleVersion,
		Profile: Profile{Name: "ka", Provider: "codex", Command: "codex"},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestTarFile(t, writer, "manifest.json", manifestData)
	writeTestTarFile(t, writer, "state/codex/auth.json", []byte(`{"tokens":"opaque"}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	got, err := extractProfileBundle(&archive, stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBundleManifest(got); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stage, "state", "codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"tokens":"opaque"}` {
		t.Fatalf("restored state = %q", data)
	}
}

func TestExtractProfileBundleRejectsTraversal(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	writeTestTarFile(t, writer, "state/../../outside", []byte("nope"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := extractProfileBundle(&archive, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsafe bundle path") {
		t.Fatalf("traversal error = %v", err)
	}
}

func writeTestTarFile(t *testing.T, writer *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
}
