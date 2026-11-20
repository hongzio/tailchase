package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveIsReproducibleChecksummedAndContainsOnlyExecutable(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "binary")
	payload := bytes.Repeat([]byte("compiled executable\x00"), 4096)
	if err := os.WriteFile(binary, payload, 0755); err != nil {
		t.Fatal(err)
	}
	one, two := filepath.Join(dir, "one.tar.gz"), filepath.Join(dir, "two.tar.gz")
	checksum, err := packBinary(binary, one)
	if err != nil {
		t.Fatal(err)
	}
	again, err := packBinary(binary, two)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(one)
	second, _ := os.ReadFile(two)
	sum := sha256.Sum256(first)
	if !bytes.Equal(first, second) || checksum != again || checksum != hex.EncodeToString(sum[:]) {
		t.Fatal("archive is not reproducible or checksum covers wrong bytes")
	}
	zipped, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	defer zipped.Close()
	archive := tar.NewReader(zipped)
	header, err := archive.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "tailchase" || header.Mode != 0755 || header.Uid != 0 || header.Gid != 0 || header.ModTime.Unix() != 0 {
		t.Fatal("archive has wrong paths, permissions or nondeterministic metadata")
	}
	content, err := io.ReadAll(archive)
	if err != nil || !bytes.Equal(content, payload) {
		t.Fatal("archive payload differs", err)
	}
	if _, err := archive.Next(); err != io.EOF {
		t.Fatal("archive contains additional files", err)
	}
}

func TestFormulaInstallsVersionedBinaryWithNoBuildDependency(t *testing.T) {
	m := manifest{releaseConfig: releaseConfig{FormulaRevision: 1, Version: "0.1.0", Revision: strings.Repeat("a", 40)}}
	for _, p := range platforms {
		data := sha256.Sum256([]byte(p[0] + "/" + p[1]))
		m.Artifacts = append(m.Artifacts, artifact{OS: p[0], Arch: p[1], File: fmt.Sprintf("tailchase_0.1.0_%s_%s.tar.gz", p[0], p[1]), SHA256: hex.EncodeToString(data[:])})
	}
	formula, err := renderFormula(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(formula, `depends_on "go"`) || strings.Contains(formula, `system "go"`) || strings.Contains(formula, "head ") {
		t.Fatal("formula builds source or selects HEAD")
	}
	if !strings.Contains(formula, "revision 1") {
		t.Fatal("formula omits upgrade revision")
	}
	if !strings.Contains(formula, `bin.install "tailchase"`) || !strings.Contains(formula, `depends_on "tailcat"`) {
		t.Fatal("formula does not install binary with runtime dependency")
	}
	for _, a := range m.Artifacts {
		if !strings.Contains(formula, "/releases/download/v0.1.0/"+a.File) || !strings.Contains(formula, a.SHA256) {
			t.Fatal("formula omits an architecture/checksum")
		}
	}
	m.Artifacts = m.Artifacts[:3]
	if _, err := renderFormula(m); err == nil {
		t.Fatal("incomplete platform matrix generated a formula")
	}
}
