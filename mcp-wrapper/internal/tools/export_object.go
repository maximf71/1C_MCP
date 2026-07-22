package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

type configurationExporter interface {
	DumpCfg(context.Context, string) error
	DumpExtensionCfg(context.Context, string, string) error
}

func RegisterExportObject(server *mcp.Server, remote *ditrix.Client, designer configurationExporter,
	project, outputRoot string) error {
	if remote == nil && designer == nil {
		return errors.New("export_object requires an EDT backend or Designer")
	}
	root, err := filepath.Abs(strings.TrimSpace(outputRoot))
	if err != nil {
		return fmt.Errorf("resolve export root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create export root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve export root links: %w", err)
	}
	server.AddTool(metadataTool("export_object", "Export the fixed target as EPF/ERF, CF or CFE. The server chooses a path below its fixed export directory; callers cannot supply arbitrary filesystem paths.", schema(map[string]any{
		"format":         enumField("Export format: auto, epf, erf, cf or cfe.", "auto", "epf", "erf", "cf", "cfe"),
		"object_name":    field("string", "Optional external data processor/report name; omit to build all external objects."),
		"extension_name": field("string", "Infobase extension name required for CFE export."),
		"file_name":      field("string", "Optional output file name without directories for CF/CFE."),
		"overwrite":      field("boolean", "Replace an existing CF/CFE artifact; default false."),
	}, "format"), localWrite("Export fixed 1C object"), func(ctx context.Context, args map[string]any) (any, error) {
		format := strings.ToLower(strings.TrimSpace(requiredString(args, "format")))
		objectName := strings.TrimSpace(requiredString(args, "object_name"))
		extensionName := strings.TrimSpace(requiredString(args, "extension_name"))
		if format == "auto" {
			switch {
			case objectName != "":
				format = "epf"
			case extensionName != "":
				format = "cfe"
			default:
				format = "cf"
			}
		}
		switch format {
		case "epf", "erf":
			if remote == nil || strings.TrimSpace(project) == "" {
				return nil, errors.New("EPF/ERF export requires the configured EDT backend and fixed project")
			}
			directory := filepath.Join(root, "external", sanitizeFileName(project))
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return nil, err
			}
			call := map[string]any{"projectName": project, "outputDir": directory, "recordBuildTime": false}
			if objectName != "" {
				call["objectName"] = objectName
			}
			build, err := remote.CallTool(ctx, "build_external_objects", call)
			if err != nil {
				return nil, err
			}
			artifacts, err := collectArtifacts(directory, "."+format)
			if err != nil {
				return nil, err
			}
			return map[string]any{"format": format, "project": project, "directory": directory, "artifacts": artifacts, "build": build}, nil
		case "cf", "cfe":
			if designer == nil {
				return nil, errors.New("CF/CFE export requires --platform and --infobase")
			}
			baseName := sanitizeFileName(project)
			if baseName == "project" {
				baseName = "configuration"
			}
			if format == "cfe" {
				if !validSimple1CName(extensionName) {
					return nil, errors.New("extension_name must be a simple 1C extension name without path characters")
				}
				baseName = sanitizeFileName(extensionName)
			}
			fileName := strings.TrimSpace(requiredString(args, "file_name"))
			if fileName == "" {
				fileName = baseName + "." + format
			}
			if filepath.Base(fileName) != fileName || strings.ToLower(filepath.Ext(fileName)) != "."+format || strings.ContainsAny(fileName, `/\\`) {
				return nil, fmt.Errorf("file_name must be a plain .%s file name without directories", format)
			}
			destination := filepath.Join(root, format, fileName)
			if !isWithin(root, destination) {
				return nil, errors.New("export destination escapes the fixed export root")
			}
			overwrite, _ := args["overwrite"].(bool)
			if _, statErr := os.Stat(destination); statErr == nil && !overwrite {
				return nil, errors.New("artifact already exists; set overwrite=true to replace it")
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return nil, err
			}
			if overwrite {
				if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
					return nil, fmt.Errorf("remove previous artifact: %w", err)
				}
			}
			var exportErr error
			if format == "cf" {
				exportErr = designer.DumpCfg(ctx, destination)
			} else {
				exportErr = designer.DumpExtensionCfg(ctx, destination, extensionName)
			}
			if exportErr != nil {
				return nil, exportErr
			}
			artifact, err := describeArtifact(destination)
			if err != nil {
				return nil, err
			}
			return map[string]any{"format": format, "artifact": artifact}, nil
		default:
			return nil, errors.New("format must be auto, epf, erf, cf or cfe")
		}
	}))
	return nil
}

func validSimple1CName(value string) bool {
	if value == "" || len(value) > 120 || strings.ContainsAny(value, `<>:"/\\|?*`) {
		return false
	}
	for index, r := range value {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= 'А' && r <= 'я' || r == 'Ё' || r == 'ё' || index > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func collectArtifacts(root, extension string) ([]map[string]any, error) {
	var result []map[string]any
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(path), extension) {
			return nil
		}
		artifact, err := describeArtifact(path)
		if err != nil {
			return err
		}
		result = append(result, artifact)
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return fmt.Sprint(result[i]["path"]) < fmt.Sprint(result[j]["path"]) })
	return result, err
}

func describeArtifact(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "size": info.Size(), "sha256": hex.EncodeToString(hash.Sum(nil))}, nil
}
