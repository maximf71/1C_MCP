package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/edt"
	"mcp-1c-analog/internal/mcp"
)

const managedExternalPrefix = "CodexExt_"

var managedExternalProjectName = regexp.MustCompile(`^[\pL_][\pL\pN_.-]{0,119}$`)

// RegisterManagedExternalObjects adds the safe EDT workflow that the generic
// fixed-project proxy deliberately does not expose. Sources and build outputs
// stay below one configured root, and only CodexExt_* projects are accepted.
func RegisterManagedExternalObjects(server *mcp.Server, bridge *edt.Client, remote *ditrix.Client,
	externalRoot string) error {
	root, err := filepath.Abs(externalRoot)
	if err != nil {
		return fmt.Errorf("resolve external objects root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("external objects root is unavailable: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("external objects root must be an existing directory: %s", root)
	}

	server.AddTool(metadataTool(
		"import_external_object_xml",
		"Create or update a managed external-object EDT project from Designer XML. The project is linked to the fixed base configuration; the infobase is not changed. source_xml must be relative to the configured external root.",
		schema(map[string]any{
			"project_name":     field("string", "Managed EDT project name starting with CodexExt_."),
			"source_xml":       field("string", "Relative path to the root EPF/ERF XML file under the configured external root."),
			"platform_version": field("string", "Optional EDT platform version; default 8.3.27."),
		}, "project_name", "source_xml"),
		localWrite("Import external object XML into EDT"),
		func(ctx context.Context, args map[string]any) (any, error) {
			project, err := requireManagedExternalProject(requiredString(args, "project_name"))
			if err != nil {
				return nil, err
			}
			relative, err := validateManagedSource(root, requiredString(args, "source_xml"))
			if err != nil {
				return nil, err
			}
			return bridge.ImportExternalObjectXML(ctx, project, relative,
				requiredString(args, "platform_version"))
		},
	))

	server.AddTool(metadataTool(
		"validate_external_object_project",
		"Refresh and revalidate one managed external-object EDT project, then return its current detailed diagnostics. It never changes the infobase.",
		schema(map[string]any{
			"project_name": field("string", "Managed EDT project name starting with CodexExt_."),
			"limit":        field("number", "Maximum diagnostics returned; default 200, maximum 1000."),
		}, "project_name"),
		localWrite("Revalidate managed external object project"),
		func(ctx context.Context, args map[string]any) (any, error) {
			project, err := requireManagedExternalProject(requiredString(args, "project_name"))
			if err != nil {
				return nil, err
			}
			if _, err := remote.CallTool(ctx, "revalidate_objects", map[string]any{
				"projectName": project, "objects": []any{},
			}); err != nil {
				return nil, err
			}
			result, err := remote.CallTool(ctx, "get_project_errors", map[string]any{
				"projectName":    project,
				"limit":          clamp(intArg(args, "limit", 200), 1, 1000),
				"responseFormat": "detailed",
			})
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		},
	))

	server.AddTool(metadataTool(
		"get_external_object_project_errors",
		"Read current EDT diagnostics for one managed external-object project without triggering a rebuild.",
		schema(map[string]any{
			"project_name": field("string", "Managed EDT project name starting with CodexExt_."),
			"limit":        field("number", "Maximum diagnostics returned; default 200, maximum 1000."),
		}, "project_name"),
		readOnly("Read managed external object diagnostics"),
		func(ctx context.Context, args map[string]any) (any, error) {
			project, err := requireManagedExternalProject(requiredString(args, "project_name"))
			if err != nil {
				return nil, err
			}
			result, err := remote.CallTool(ctx, "get_project_errors", map[string]any{
				"projectName":    project,
				"limit":          clamp(intArg(args, "limit", 200), 1, 1000),
				"responseFormat": "detailed",
			})
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		},
	))

	server.AddTool(metadataTool(
		"build_external_object_project",
		"Build all or one object from a managed EDT external-object project. Output is forced to .edt-external-builds/<project> below the configured root and build-time metadata stamping is disabled.",
		schema(map[string]any{
			"project_name": field("string", "Managed EDT project name starting with CodexExt_."),
			"object_name":  field("string", "Optional external data processor/report name. Omit to build all."),
		}, "project_name"),
		localWrite("Build managed external object project"),
		func(ctx context.Context, args map[string]any) (any, error) {
			project, err := requireManagedExternalProject(requiredString(args, "project_name"))
			if err != nil {
				return nil, err
			}
			output := filepath.Join(root, ".edt-external-builds", project)
			if err := os.MkdirAll(output, 0o755); err != nil {
				return nil, fmt.Errorf("create managed build directory: %w", err)
			}
			resolvedOutput, err := filepath.EvalSymlinks(output)
			if err != nil || !isWithin(root, resolvedOutput) {
				return nil, fmt.Errorf("managed build directory escapes the configured external root")
			}
			call := map[string]any{
				"projectName":     project,
				"outputDir":       resolvedOutput,
				"recordBuildTime": false,
			}
			if object := strings.TrimSpace(requiredString(args, "object_name")); object != "" {
				call["objectName"] = object
			}
			result, err := remote.CallTool(ctx, "build_external_objects", call)
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		},
	))

	server.AddTool(metadataTool(
		"generate_external_object",
		"Run the complete managed EDT pipeline for an existing EPF/ERF XML source: import, revalidate, read diagnostics and optionally build. The source and output remain below the fixed external root; the infobase is not changed.",
		schema(map[string]any{
			"project_name":     field("string", "Managed EDT project name starting with CodexExt_."),
			"source_xml":       field("string", "Relative path to the root EPF/ERF XML file under the configured external root."),
			"platform_version": field("string", "Optional EDT platform version; default is defined by the EDT bridge."),
			"object_name":      field("string", "Optional external object name to build."),
			"build":            field("boolean", "Build after import and validation; default true."),
		}, "project_name", "source_xml"),
		localWrite("Generate and build managed external object"),
		func(ctx context.Context, args map[string]any) (any, error) {
			project, err := requireManagedExternalProject(requiredString(args, "project_name"))
			if err != nil {
				return nil, err
			}
			relative, err := validateManagedSource(root, requiredString(args, "source_xml"))
			if err != nil {
				return nil, err
			}
			importResult, err := bridge.ImportExternalObjectXML(ctx, project, relative, requiredString(args, "platform_version"))
			if err != nil {
				return nil, err
			}
			if _, err := remote.CallTool(ctx, "revalidate_objects", map[string]any{"projectName": project, "objects": []any{}}); err != nil {
				return nil, err
			}
			diagnostics, err := remote.CallTool(ctx, "get_project_errors", map[string]any{
				"projectName": project, "limit": 1000, "responseFormat": "detailed",
			})
			if err != nil {
				return nil, err
			}
			buildRequested := true
			if supplied, ok := args["build"].(bool); ok {
				buildRequested = supplied
			}
			result := map[string]any{"project_name": project, "import": importResult, "diagnostics": diagnostics, "built": false}
			if !buildRequested {
				return result, nil
			}
			buildResult, output, err := buildManagedExternal(ctx, remote, root, project, requiredString(args, "object_name"))
			if err != nil {
				return nil, err
			}
			result["build"] = buildResult
			result["output"] = output
			result["built"] = true
			return result, nil
		},
	))
	return nil
}

func buildManagedExternal(ctx context.Context, remote *ditrix.Client, root, project, object string) (map[string]any, string, error) {
	output := filepath.Join(root, ".edt-external-builds", project)
	if err := os.MkdirAll(output, 0o755); err != nil {
		return nil, "", fmt.Errorf("create managed build directory: %w", err)
	}
	resolvedOutput, err := filepath.EvalSymlinks(output)
	if err != nil || !isWithin(root, resolvedOutput) {
		return nil, "", fmt.Errorf("managed build directory escapes the configured external root")
	}
	call := map[string]any{"projectName": project, "outputDir": resolvedOutput, "recordBuildTime": false}
	if object = strings.TrimSpace(object); object != "" {
		call["objectName"] = object
	}
	result, err := remote.CallTool(ctx, "build_external_objects", call)
	return result, resolvedOutput, err
}

func requireManagedExternalProject(project string) (string, error) {
	project = strings.TrimSpace(project)
	if !strings.HasPrefix(project, managedExternalPrefix) || len(project) == len(managedExternalPrefix) ||
		!managedExternalProjectName.MatchString(project) {
		return "", fmt.Errorf("project_name must be a valid EDT project name starting with %s", managedExternalPrefix)
	}
	return project, nil
}

func validateManagedSource(root, relative string) (string, error) {
	relative = strings.TrimSpace(relative)
	if relative == "" || filepath.IsAbs(relative) || strings.ToLower(filepath.Ext(relative)) != ".xml" {
		return "", fmt.Errorf("source_xml must be a relative XML file below the configured external root")
	}
	joined := filepath.Join(root, filepath.Clean(relative))
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("source_xml is unavailable: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || !isWithin(root, real) {
		return "", fmt.Errorf("source_xml must be a regular XML file below the configured external root")
	}
	cleanRelative, err := filepath.Rel(root, real)
	if err != nil || strings.HasPrefix(cleanRelative, "..") {
		return "", fmt.Errorf("source_xml escapes the configured external root")
	}
	return cleanRelative, nil
}

func isWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
