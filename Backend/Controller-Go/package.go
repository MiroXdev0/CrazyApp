package main

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type localPackageManifest struct {
	TaskPackageManifest
	Executable string `json:"executable,omitempty"`
}

func buildTaskPackage(root string) (string, localPackageManifest, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", localPackageManifest{}, err
	}
	if !info.IsDir() {
		return "", localPackageManifest{}, errors.New("task package root must be a directory")
	}
	manifest := localPackageManifest{}
	manifestPath := filepath.Join(root, "nodren.json")
	if data, readErr := os.ReadFile(manifestPath); readErr == nil {
		if err := json.Unmarshal(data, &manifest); err != nil {
			return "", manifest, fmt.Errorf("invalid nodren.json: %w", err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", manifest, readErr
	}
	if strings.TrimSpace(manifest.EntryPoint) == "" {
		for _, candidate := range []string{"run.py", "main.py", "app.py", "main.js", "index.js", "run.sh", "app.exe", "app"} {
			if _, statErr := os.Stat(filepath.Join(root, candidate)); statErr == nil {
				manifest.EntryPoint = candidate
				break
			}
		}
	}
	if strings.TrimSpace(manifest.EntryPoint) == "" {
		return "", manifest, errors.New("folder package needs nodren.json with entry_point or a known entry file")
	}
	if strings.Contains(filepath.ToSlash(manifest.EntryPoint), "..") || filepath.IsAbs(manifest.EntryPoint) {
		return "", manifest, errors.New("package entry_point must stay inside the package")
	}
	if manifest.Runtime == "" {
		switch strings.ToLower(filepath.Ext(manifest.EntryPoint)) {
		case ".py":
			manifest.Runtime = "python"
		case ".js", ".mjs", ".cjs":
			manifest.Runtime = "node"
		case ".sh":
			manifest.Runtime = "sh"
		case ".ps1":
			manifest.Runtime = "powershell"
		}
	}
	if manifest.Runtime == "" && manifest.Executable == "" {
		manifest.Executable = "./" + filepath.ToSlash(manifest.EntryPoint)
	}
	temporary, err := os.CreateTemp("", "nodren-package-*.tar")
	if err != nil {
		return "", manifest, err
	}
	name := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(name)
		}
	}()
	writer := tar.NewWriter(temporary)
	data, err := json.Marshal(manifest)
	if err != nil {
		temporary.Close()
		return "", manifest, err
	}
	if err := writeTarBytes(writer, "nodren.manifest.json", data, 0o644); err != nil {
		temporary.Close()
		return "", manifest, err
	}
	err = filepath.Walk(root, func(path string, fileInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." || filepath.Clean(relative) == "nodren.json" {
			return nil
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in task packages: %s", relative)
		}
		tarName := filepath.ToSlash(relative)
		header, err := tar.FileInfoHeader(fileInfo, "")
		if err != nil {
			return err
		}
		header.Name = tarName
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if fileInfo.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err == nil {
		err = writer.Close()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", manifest, err
	}
	cleanup = false
	return name, manifest, nil
}

func writeTarBytes(writer *tar.Writer, name string, data []byte, mode int64) error {
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}
