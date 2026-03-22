package orchestrator

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"scagent/internal/skill"
)

var pluginDirSanitizer = regexp.MustCompile(`[^a-z0-9._-]+`)

func (s *Service) refreshSkills() error {
	if s == nil || s.skills == nil {
		return nil
	}
	return s.skills.Reload()
}

func (s *Service) PluginBundles() ([]skill.PluginBundle, error) {
	if err := s.refreshSkills(); err != nil {
		return nil, err
	}
	return s.skills.Bundles(), nil
}

func (s *Service) UploadPluginBundle(filename string, reader io.Reader) (*skill.PluginBundle, error) {
	pluginRoot := s.pluginRoot()
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create plugin root: %w", err)
	}

	archiveFile, err := os.CreateTemp(pluginRoot, "plugin-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create temp plugin archive: %w", err)
	}
	archivePath := archiveFile.Name()
	defer os.Remove(archivePath)

	if _, err := io.Copy(archiveFile, reader); err != nil {
		archiveFile.Close()
		return nil, fmt.Errorf("copy plugin archive: %w", err)
	}
	if err := archiveFile.Close(); err != nil {
		return nil, fmt.Errorf("close plugin archive: %w", err)
	}

	extractRoot, err := os.MkdirTemp(pluginRoot, "plugin-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create temp plugin directory: %w", err)
	}
	cleanupExtractRoot := true
	defer func() {
		if cleanupExtractRoot {
			_ = os.RemoveAll(extractRoot)
		}
	}()

	if err := unzipArchive(archivePath, extractRoot); err != nil {
		return nil, err
	}

	manifestPath, err := locatePluginManifest(extractRoot)
	if err != nil {
		return nil, err
	}
	bundle, err := skill.LoadPluginBundleFile(manifestPath)
	if err != nil {
		return nil, err
	}

	sourceDir := filepath.Dir(manifestPath)
	targetDir := filepath.Join(pluginRoot, sanitizePluginDir(bundle.ID, filename))
	if err := os.RemoveAll(targetDir); err != nil {
		return nil, fmt.Errorf("remove previous plugin bundle: %w", err)
	}
	if err := os.Rename(sourceDir, targetDir); err != nil {
		return nil, fmt.Errorf("move plugin bundle into hub: %w", err)
	}

	cleanupExtractRoot = false
	_ = os.RemoveAll(extractRoot)

	if err := s.refreshSkills(); err != nil {
		_ = os.RemoveAll(targetDir)
		_ = s.refreshSkills()
		return nil, err
	}

	for _, loaded := range s.skills.Bundles() {
		if loaded.ID == bundle.ID {
			return &loaded, nil
		}
	}
	return nil, fmt.Errorf("uploaded plugin %q was not registered", bundle.ID)
}

func (s *Service) pluginRoot() string {
	return filepath.Join(s.dataRoot, "skill-hub", "plugins")
}

func unzipArchive(path, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open plugin archive: %w", err)
	}
	defer reader.Close()

	destPrefix := filepath.Clean(dest) + string(os.PathSeparator)
	for _, file := range reader.File {
		cleanName := filepath.Clean(file.Name)
		if cleanName == "." || cleanName == "" {
			continue
		}
		if filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("plugin archive contains invalid path %q", file.Name)
		}

		targetPath := filepath.Join(dest, cleanName)
		cleanTarget := filepath.Clean(targetPath)
		if cleanTarget != filepath.Clean(dest) && !strings.HasPrefix(cleanTarget, destPrefix) {
			return fmt.Errorf("plugin archive escaped target directory: %q", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return fmt.Errorf("create plugin directory: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
			return fmt.Errorf("create plugin parent directory: %w", err)
		}

		in, err := file.Open()
		if err != nil {
			return fmt.Errorf("open plugin file %q: %w", file.Name, err)
		}
		out, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			in.Close()
			return fmt.Errorf("create plugin file %q: %w", file.Name, err)
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			in.Close()
			return fmt.Errorf("write plugin file %q: %w", file.Name, err)
		}
		if err := out.Close(); err != nil {
			in.Close()
			return fmt.Errorf("close plugin file %q: %w", file.Name, err)
		}
		if err := in.Close(); err != nil {
			return fmt.Errorf("close plugin archive entry %q: %w", file.Name, err)
		}
	}
	return nil
}

func locatePluginManifest(root string) (string, error) {
	manifests := make([]string, 0, 2)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.EqualFold(info.Name(), "plugin.json") {
			manifests = append(manifests, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan extracted plugin bundle: %w", err)
	}
	if len(manifests) == 0 {
		return "", fmt.Errorf("plugin archive is missing plugin.json")
	}
	sort.Slice(manifests, func(i, j int) bool {
		depthI := strings.Count(filepath.ToSlash(manifests[i]), "/")
		depthJ := strings.Count(filepath.ToSlash(manifests[j]), "/")
		if depthI == depthJ {
			return manifests[i] < manifests[j]
		}
		return depthI < depthJ
	})
	return manifests[0], nil
}

func sanitizePluginDir(bundleID, filename string) string {
	candidate := strings.ToLower(strings.TrimSpace(bundleID))
	if candidate == "" {
		candidate = strings.ToLower(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	}
	candidate = pluginDirSanitizer.ReplaceAllString(candidate, "-")
	candidate = strings.Trim(candidate, "-.")
	if candidate == "" {
		return "plugin-bundle"
	}
	return candidate
}
