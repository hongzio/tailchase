// Release builds happen on the maintainer/CI machine. Homebrew installs only the
// resulting binary and never invokes Go on the user's machine.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type releaseConfig struct {
	FormulaRevision int    `json:"formula_revision,omitempty"`
	Version         string `json:"version"`
	Revision        string `json:"revision"`
}
type artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	releaseConfig
	Artifacts []artifact `json:"artifacts"`
}

var platforms = [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", "packaging/release.json", "release version and source commit")
	out := flag.String("output", "dist", "artifact directory")
	formula := flag.String("formula", "packaging/tailchase.rb", "generated binary formula")
	flag.Parse()
	data, err := os.ReadFile(*configPath)
	if err != nil {
		return err
	}
	var c releaseConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.FormulaRevision < 0 {
		return fmt.Errorf("formula revision must not be negative")
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(c.Version) || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(c.Revision) {
		return fmt.Errorf("version must be X.Y.Z and revision a full Git commit")
	}
	work, err := os.MkdirTemp("", "tailchase-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := extractSource(c.Revision, work); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	m := manifest{releaseConfig: c, Artifacts: []artifact{}}
	var sums strings.Builder
	for _, platform := range platforms {
		targetOS, targetArch := platform[0], platform[1]
		fmt.Fprintf(os.Stderr, "Building %s/%s from %s\n", targetOS, targetArch, c.Revision[:12])
		binary := filepath.Join(work, "tailchase")
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", binary, "./cmd/tailchase")
		cmd.Dir = work
		cmd.Env = buildEnv(targetOS, targetArch)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		name := fmt.Sprintf("tailchase_%s_%s_%s.tar.gz", c.Version, targetOS, targetArch)
		checksum, err := packBinary(binary, filepath.Join(*out, name))
		if err != nil {
			return err
		}
		m.Artifacts = append(m.Artifacts, artifact{targetOS, targetArch, name, checksum})
		fmt.Fprintf(&sums, "%s  %s\n", checksum, name)
	}
	if err := os.WriteFile(filepath.Join(*out, "checksums.txt"), []byte(sums.String()), 0644); err != nil {
		return err
	}
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "release-manifest.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	ruby, err := renderFormula(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*formula, []byte(ruby), 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Generated %s and four checksummed release archives in %s\n", *formula, *out)
	return nil
}
func buildEnv(targetOS, targetArch string) []string {
	env := []string{}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		switch name {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOARM64", "GOFLAGS":
			continue
		}
		env = append(env, value)
	}
	return append(env, "GOOS="+targetOS, "GOARCH="+targetArch, "CGO_ENABLED=0", "GOAMD64=v1", "GOARM64=v8.0", "GOFLAGS=")
}
func extractSource(revision, dir string) error {
	cmd := exec.Command("git", "archive", "--format=tar", revision, "--", "go.mod", "go.sum", "cmd", "internal")
	var archive bytes.Buffer
	cmd.Stdout = &archive
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	reader := tar.NewReader(&archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe source archive path")
		}
		dest := filepath.Join(dir, name)
		switch header.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			file, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported source archive entry")
		}
	}
}
func packBinary(binary, dest string) (string, error) {
	source, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", err
	}
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	zip := gzip.NewWriter(io.MultiWriter(out, hash))
	zip.Header.OS = 255
	archive := tar.NewWriter(zip)
	err = archive.WriteHeader(&tar.Header{Name: "tailchase", Mode: 0755, Size: info.Size(), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR, Typeflag: tar.TypeReg})
	if err == nil {
		_, err = io.Copy(archive, source)
	}
	tarErr := archive.Close()
	zipErr := zip.Close()
	closeErr := out.Close()
	for _, e := range []error{err, tarErr, zipErr, closeErr} {
		if e != nil {
			os.Remove(dest)
			return "", e
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func renderFormula(m manifest) (string, error) {
	checksums := map[string]artifact{}
	for _, a := range m.Artifacts {
		checksums[a.OS+"/"+a.Arch] = a
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by packaging/release; checksums cover the exact release archives.\nclass Tailchase < Formula\n  desc \"Manage tailcat aliases, keys, and persistent connections\"\n  homepage \"https://github.com/hongzio/tailchase\"\n  version %q\n\n", m.Version)
	if m.FormulaRevision > 0 {
		fmt.Fprintf(&b, "  revision %d\n\n", m.FormulaRevision)
	}
	for _, platform := range []struct{ goOS, brewOS string }{{"darwin", "macos"}, {"linux", "linux"}} {
		fmt.Fprintf(&b, "  on_%s do\n", platform.brewOS)
		for _, arch := range []struct{ goArch, brewArch string }{{"arm64", "arm"}, {"amd64", "intel"}} {
			a, ok := checksums[platform.goOS+"/"+arch.goArch]
			if !ok {
				return "", fmt.Errorf("missing artifact %s/%s", platform.goOS, arch.goArch)
			}
			fmt.Fprintf(&b, "    on_%s do\n      url \"https://github.com/hongzio/tailchase/releases/download/v%s/%s\"\n      sha256 %q\n    end\n", arch.brewArch, m.Version, a.File, a.SHA256)
		}
		b.WriteString("  end\n\n")
	}
	b.WriteString(`  depends_on "tailcat"

  def install
    bin.install "tailchase"
  end

  service do
    run [opt_bin/"tailchase", "daemon"]
    environment_variables PATH: "#{HOMEBREW_PREFIX}/bin:/usr/bin:/bin:/usr/sbin:/sbin"
    keep_alive true
    log_path var/"log/tailchase.log"
    error_log_path var/"log/tailchase.log"
  end

  test do
    assert_match "tailchase", shell_output("#{bin}/tailchase --help")
    assert_match "daemon: offline", shell_output("TAILCHASE_CONFIG_DIR=#{testpath}/config TAILCHASE_TAILCAT_KEYS_DIR= #{bin}/tailchase status")
  end
end
`)
	return b.String(), nil
}
