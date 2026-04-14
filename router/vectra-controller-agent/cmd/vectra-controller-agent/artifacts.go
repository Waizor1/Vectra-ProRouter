package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type artifactJob struct {
	ArtifactURL       string
	SHA256            string
	SignatureURL      string
	ArtifactVersion   string
	PackageList       []string
	PackageArtifacts  []packageArtifact
	Channel           string
	ValidationCommand string
}

type packageArtifact struct {
	Name            string
	ArtifactURL     string
	SHA256          string
	SignatureURL    string
	ArtifactVersion string
}

type stagedArtifact struct {
	URL           string
	Path          string
	SHA256        string
	SignaturePath string
}

type packageIndexEntry struct {
	Package   string
	Version   string
	Filename  string
	SHA256Sum string
}

func downloadTimeout(base time.Duration) time.Duration {
	if base < 2*time.Minute {
		return 2 * time.Minute
	}
	return base
}

func parseArtifactJob(payload map[string]interface{}, defaultPackages []string) artifactJob {
	packageArtifacts := parsePackageArtifacts(payload)
	artifactVersion := payloadString(payload, "artifactVersion")
	if artifactVersion == "" && len(packageArtifacts) > 0 {
		artifactVersion = packageArtifacts[0].ArtifactVersion
	}

	return artifactJob{
		ArtifactURL:       payloadString(payload, "artifactUrl"),
		SHA256:            payloadString(payload, "sha256"),
		SignatureURL:      payloadString(payload, "signatureUrl"),
		ArtifactVersion:   artifactVersion,
		PackageList:       fallbackStrings(firstNonEmptyStrings(payloadStringSlice(payload, "packageList"), payloadStringSlice(payload, "packages")), defaultPackages),
		PackageArtifacts:  packageArtifacts,
		Channel:           payloadString(payload, "channel"),
		ValidationCommand: payloadString(payload, "validationCommand"),
	}
}

func parsePackageArtifacts(payload map[string]interface{}) []packageArtifact {
	if payload == nil {
		return nil
	}

	raw, ok := payload["packageArtifacts"]
	if !ok {
		return nil
	}

	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	artifacts := make([]packageArtifact, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		name := payloadString(entry, "name")
		artifactURL := payloadString(entry, "artifactUrl")
		if name == "" || artifactURL == "" {
			continue
		}

		artifacts = append(artifacts, packageArtifact{
			Name:            name,
			ArtifactURL:     artifactURL,
			SHA256:          payloadString(entry, "sha256"),
			SignatureURL:    payloadString(entry, "signatureUrl"),
			ArtifactVersion: payloadString(entry, "artifactVersion"),
		})
	}

	return artifacts
}

func stageArtifact(
	ctx context.Context,
	targetURL string,
	expectedSHA256 string,
	signatureURL string,
	timeout time.Duration,
) (stagedArtifact, error) {
	if strings.TrimSpace(targetURL) == "" {
		return stagedArtifact{}, fmt.Errorf("artifact url is required")
	}

	stagingDir, err := os.MkdirTemp("", "vectra-artifact-*")
	if err != nil {
		return stagedArtifact{}, fmt.Errorf("create staging directory: %w", err)
	}

	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return stagedArtifact{}, fmt.Errorf("parse artifact url: %w", err)
	}
	fileName := path.Base(parsedURL.Path)
	if fileName == "." || fileName == "/" || fileName == "" {
		fileName = "artifact.bin"
	}

	artifactPath := filepath.Join(stagingDir, fileName)
	if err := downloadFile(ctx, targetURL, artifactPath, timeout); err != nil {
		return stagedArtifact{}, err
	}

	if expectedSHA256 != "" {
		actualSHA256, err := fileSHA256(artifactPath)
		if err != nil {
			return stagedArtifact{}, err
		}
		if !strings.EqualFold(actualSHA256, expectedSHA256) {
			return stagedArtifact{}, fmt.Errorf(
				"artifact checksum mismatch for %s: got %s want %s",
				targetURL,
				actualSHA256,
				expectedSHA256,
			)
		}
	}

	staged := stagedArtifact{
		URL:    targetURL,
		Path:   artifactPath,
		SHA256: expectedSHA256,
	}

	if strings.TrimSpace(signatureURL) != "" {
		signaturePath := filepath.Join(stagingDir, fileName+".sig")
		if err := downloadFile(ctx, signatureURL, signaturePath, timeout); err != nil {
			return stagedArtifact{}, err
		}
		if err := verifyUsignSignature(artifactPath, signaturePath); err != nil {
			return stagedArtifact{}, err
		}
		staged.SignaturePath = signaturePath
	}

	return staged, nil
}

func stagePackageArtifacts(
	ctx context.Context,
	job artifactJob,
	timeout time.Duration,
) ([]stagedArtifact, error) {
	if len(job.PackageList) == 0 {
		return nil, fmt.Errorf("package list is required")
	}

	if len(job.PackageArtifacts) > 0 {
		byName := make(map[string]packageArtifact, len(job.PackageArtifacts))
		for _, artifact := range job.PackageArtifacts {
			if artifact.Name == "" || artifact.ArtifactURL == "" {
				continue
			}
			byName[artifact.Name] = artifact
		}

		staged := make([]stagedArtifact, 0, len(job.PackageList))
		for _, packageName := range job.PackageList {
			artifact, ok := byName[packageName]
			if !ok {
				return nil, fmt.Errorf("package %s missing from explicit packageArtifacts payload", packageName)
			}

			stagedArtifact, err := stageArtifact(
				ctx,
				artifact.ArtifactURL,
				artifact.SHA256,
				artifact.SignatureURL,
				timeout,
			)
			if err != nil {
				return nil, err
			}
			staged = append(staged, stagedArtifact)
		}

		return staged, nil
	}

	if job.ArtifactURL == "" {
		return nil, fmt.Errorf("artifactUrl is required for package update jobs")
	}

	artifactURL, err := url.Parse(job.ArtifactURL)
	if err != nil {
		return nil, fmt.Errorf("parse package artifact url: %w", err)
	}

	artifactURL.Path = path.Dir(artifactURL.Path)
	artifactURL.RawQuery = ""
	artifactURL.Fragment = ""
	feedDirURL := strings.TrimRight(artifactURL.String(), "/")

	packageIndexPath, signaturePath, err := stagePackageIndex(ctx, feedDirURL, job.SignatureURL, timeout)
	if err != nil {
		return nil, err
	}

	entries, err := parsePackagesIndex(packageIndexPath)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]packageIndexEntry, len(entries))
	for _, entry := range entries {
		if entry.Package == "" || entry.Filename == "" {
			continue
		}
		if job.ArtifactVersion != "" && entry.Version != job.ArtifactVersion {
			continue
		}
		if _, exists := byName[entry.Package]; !exists {
			byName[entry.Package] = entry
		}
	}

	staged := make([]stagedArtifact, 0, len(job.PackageList))
	for _, packageName := range job.PackageList {
		entry, ok := byName[packageName]
		if !ok {
			return nil, fmt.Errorf("package %s@%s not found in feed index", packageName, job.ArtifactVersion)
		}

		packageURL := joinURL(feedDirURL, entry.Filename)
		artifact, err := stageArtifact(ctx, packageURL, entry.SHA256Sum, "", timeout)
		if err != nil {
			return nil, err
		}

		if strings.EqualFold(packageURL, job.ArtifactURL) && job.SHA256 != "" {
			actualSHA256, err := fileSHA256(artifact.Path)
			if err != nil {
				return nil, err
			}
			if !strings.EqualFold(actualSHA256, job.SHA256) {
				return nil, fmt.Errorf(
					"primary artifact checksum mismatch for %s: got %s want %s",
					job.ArtifactURL,
					actualSHA256,
					job.SHA256,
				)
			}
		}

		if signaturePath != "" {
			artifact.SignaturePath = signaturePath
		}
		staged = append(staged, artifact)
	}

	return staged, nil
}

func stagePackageIndex(
	ctx context.Context,
	feedDirURL string,
	explicitSignatureURL string,
	timeout time.Duration,
) (string, string, error) {
	stagingDir, err := os.MkdirTemp("", "vectra-feed-*")
	if err != nil {
		return "", "", fmt.Errorf("create feed staging directory: %w", err)
	}

	indexPath := filepath.Join(stagingDir, "Packages")
	if err := downloadFile(ctx, joinURL(feedDirURL, "Packages"), indexPath, timeout); err != nil {
		return "", "", err
	}

	signatureURL := explicitSignatureURL
	if signatureURL == "" {
		signatureURL = joinURL(feedDirURL, "Packages.sig")
	}

	signaturePath := filepath.Join(stagingDir, "Packages.sig")
	if downloadErr := downloadFile(ctx, signatureURL, signaturePath, timeout); downloadErr == nil {
		if err := verifyUsignSignature(indexPath, signaturePath); err != nil {
			return "", "", err
		}
		return indexPath, signaturePath, nil
	} else if explicitSignatureURL != "" {
		return "", "", fmt.Errorf("download package index signature: %w", downloadErr)
	}

	return indexPath, "", nil
}

func parsePackagesIndex(indexPath string) ([]packageIndexEntry, error) {
	file, err := os.Open(indexPath)
	if err != nil {
		return nil, fmt.Errorf("open Packages index: %w", err)
	}
	defer file.Close()

	entries := []packageIndexEntry{}
	current := packageIndexEntry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if current.Package != "" {
				entries = append(entries, current)
				current = packageIndexEntry{}
			}
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Package":
			current.Package = value
		case "Version":
			current.Version = value
		case "Filename":
			current.Filename = value
		case "SHA256sum":
			current.SHA256Sum = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Packages index: %w", err)
	}
	if current.Package != "" {
		entries = append(entries, current)
	}

	return entries, nil
}

func downloadFile(ctx context.Context, sourceURL string, destinationPath string, timeout time.Duration) error {
	if strings.TrimSpace(sourceURL) == "" {
		return fmt.Errorf("download url is required")
	}

	client := &http.Client{Timeout: downloadTimeout(timeout)}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("create download request for %s: %w", sourceURL, err)
	}
	request.Header.Set("User-Agent", "vectra-controller-agent/2026-04-v1")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", sourceURL, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download %s: unexpected status %d", sourceURL, response.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	file, err := os.Create(destinationPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destinationPath, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, response.Body); err != nil {
		return fmt.Errorf("write %s: %w", destinationPath, err)
	}

	return nil
}

func fileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filePath, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", filePath, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyUsignSignature(messagePath string, signaturePath string) error {
	if _, err := exec.LookPath("usign"); err != nil {
		return fmt.Errorf("signature verification requested but usign is not available")
	}

	keyDir := "/etc/opkg/keys"
	if info, err := os.Stat(keyDir); err != nil || !info.IsDir() {
		return fmt.Errorf("signature verification requested but %s is not available", keyDir)
	}

	command := exec.Command("usign", "-V", "-P", keyDir, "-m", messagePath, "-x", signaturePath)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"verify usign signature for %s: %w (%s)",
			messagePath,
			err,
			strings.TrimSpace(string(output)),
		)
	}

	return nil
}

func joinURL(base string, relative string) string {
	base = strings.TrimRight(base, "/")
	relative = strings.TrimLeft(relative, "/")
	if relative == "" {
		return base
	}
	return base + "/" + relative
}

func fallbackStrings(value []string, fallback []string) []string {
	if len(value) > 0 {
		return value
	}

	copied := make([]string, len(fallback))
	copy(copied, fallback)
	return copied
}

func firstNonEmptyStrings(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}
