package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mcp-1c-analog/internal/configmerge"
	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
	"mcp-1c-analog/internal/techlog"
	"mcp-1c-analog/internal/vanessa"
)

func registerLargeSubsystems(server *mcp.Server, remote *ditrix.Client, project string, available map[string]bool, options DitrixRegistrationOptions) {
	diagnostics := &techlog.Manager{ConfigPath: options.TechlogConfig, LogRoot: options.TechlogRoot, WorkDir: options.WorkDir}
	addIfMissing(server, diagnosticsTool(remote, project, available, diagnostics, options))
	va := &vanessa.Manager{Platform: options.VanessaPlatform, Infobase: options.VanessaInfobase, Runner: options.VanessaRunner, FeaturesRoot: options.VanessaFeaturesRoot, StepsRoot: options.VanessaStepsRoot, WorkDir: options.WorkDir}
	addIfMissing(server, vanessaTool(va))
	merge := &configmerge.Manager{SourceRoot: options.ConfigurationSourceRoot, WorkDir: options.WorkDir, Designer: options.Designer}
	addIfMissing(server, updateConfigurationTool(remote, project, available, merge))
}

func diagnosticsTool(remote *ditrix.Client, project string, available map[string]bool, manager *techlog.Manager, options DitrixRegistrationOptions) mcp.Tool {
	return mcp.Tool{Name: "diagnostics", Description: "Unified fixed-target performance, technological-log and event-log diagnostics. Enabling, disabling and clearing the technological log require confirm=true.", InputSchema: schema(map[string]any{
		"operation": enumField("Diagnostics operation.", "status", "measureStart", "measureStop", "measureResults", "measureCoverage", "measureCallers", "techlogEnable", "techlogAnalyze", "techlogDisable", "techlogClear", "eventlogRead", "eventlogSummary"),
		"arguments": map[string]any{"type": "object", "additionalProperties": true}, "confirm": field("boolean", "Required for technological-log configuration changes and cleanup."),
	}, "operation"), Annotations: localWrite("Run fixed-target diagnostics"), Handler: func(ctx context.Context, args map[string]any) (any, error) {
		op := requiredString(args, "operation")
		call, err := nestedArguments(args)
		if err != nil {
			return nil, err
		}
		switch op {
		case "status":
			return map[string]any{"project": project, "techlog": manager.Status(), "performance_backend": available["start_profiling"], "event_log_backend": options.LiveClient != nil}, nil
		case "measureStart", "measureStop", "measureResults", "measureCoverage", "measureCallers":
			mapping := map[string]string{"measureStart": "start_profiling", "measureStop": "stop_profiling", "measureResults": "get_profiling_results", "measureCoverage": "get_profiling_results", "measureCallers": "get_profiling_results"}
			tool := mapping[op]
			if !available[tool] {
				return nil, fmt.Errorf("%s is unavailable in the installed EDT backend", op)
			}
			call["projectName"] = project
			if op == "measureCoverage" {
				call["view"] = "coverage"
			}
			if op == "measureCallers" {
				call["view"] = "callers"
			}
			result, err := remote.CallTool(ctx, tool, call)
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		case "techlogEnable":
			if !boolValue(args["confirm"]) {
				return map[string]any{"dry_run": true, "preset": requiredString(call, "preset"), "minimum_duration_ms": floatValue(call["minimumDurationMs"]), "status": manager.Status()}, nil
			}
			return manager.Enable(requiredString(call, "preset"), floatValue(call["minimumDurationMs"]), intValue(call["historyHours"], 24))
		case "techlogAnalyze":
			return manager.Analyze(floatValue(call["minimumDurationMs"]), requiredString(call, "moduleFilter"), intValue(call["limit"], 50))
		case "techlogDisable":
			if !boolValue(args["confirm"]) {
				return nil, errors.New("techlogDisable requires confirm=true")
			}
			return manager.Disable()
		case "techlogClear":
			if !boolValue(args["confirm"]) {
				return nil, errors.New("techlogClear requires confirm=true")
			}
			return manager.Clear()
		case "eventlogRead", "eventlogSummary":
			if options.LiveClient == nil {
				return nil, errors.New("event log requires the fixed live 1C endpoint")
			}
			var value any
			if err := options.LiveClient.Post(ctx, "/eventlog", call, &value); err != nil {
				return nil, err
			}
			if op == "eventlogSummary" {
				return summarizeEventLog(value), nil
			}
			return value, nil
		default:
			return nil, errors.New("unknown diagnostics operation")
		}
	}}
}

func vanessaTool(manager *vanessa.Manager) mcp.Tool {
	return mcp.Tool{Name: "vanessa", Description: "Vanessa Automation subsystem locked to configured platform, file infobase, runner and feature roots. Step discovery and syntax checks do not launch 1C; run requires confirm=true.", InputSchema: schema(map[string]any{"operation": enumField("Vanessa operation.", "status", "steps", "checkSyntax", "run"), "arguments": map[string]any{"type": "object", "additionalProperties": true}, "confirm": field("boolean", "Required to launch Vanessa Automation.")}, "operation"), Annotations: localWrite("Validate or run Vanessa Automation"), Handler: func(ctx context.Context, args map[string]any) (any, error) {
		op := requiredString(args, "operation")
		call, err := nestedArguments(args)
		if err != nil {
			return nil, err
		}
		switch op {
		case "status":
			return manager.Status(), nil
		case "steps":
			return manager.Steps(requiredString(call, "query"), intValue(call["limit"], 200))
		case "checkSyntax":
			return manager.CheckSyntax(requiredString(call, "featurePath"))
		case "run":
			if !boolValue(args["confirm"]) {
				return map[string]any{"dry_run": true, "operation": "run", "arguments": call, "status": manager.Status()}, nil
			}
			tags, err := stringArray(call["tags"])
			if err != nil {
				return nil, fmt.Errorf("tags: %w", err)
			}
			ignore, err := stringArray(call["ignoreTags"])
			if err != nil {
				return nil, fmt.Errorf("ignoreTags: %w", err)
			}
			return manager.Run(ctx, requiredString(call, "featurePath"), tags, ignore, boolValue(call["keepOpen"]), boolValue(call["screenshotOnFailure"]))
		default:
			return nil, errors.New("unknown Vanessa operation")
		}
	}}
}

func updateConfigurationTool(remote *ditrix.Client, project string, available map[string]bool, manager *configmerge.Manager) mcp.Tool {
	return mcp.Tool{Name: "update_configuration", Description: "Managed configuration comparison and merge. Uses native EDT support when available; otherwise provides a guarded source-tree mode with dry-run, fingerprints and snapshots. Provider-support metadata requires native EDT 2026.1+.", InputSchema: schema(map[string]any{"operation": enumField("Update operation.", "help", "prepareSource", "compare", "differences", "merge", "updateVendor", "exportDifferences", "replayCustomizations", "status", "cancel", "cleanupSources"), "arguments": map[string]any{"type": "object", "additionalProperties": true}, "confirm": field("boolean", "Required for merge, updateVendor and cleanupSources."), "dryRun": field("boolean", "Preview mutations; defaults to true.")}, "operation"), Annotations: localWrite("Compare and merge the fixed EDT project"), Handler: func(ctx context.Context, args map[string]any) (any, error) {
		op := requiredString(args, "operation")
		call, err := nestedArguments(args)
		if err != nil {
			return nil, err
		}
		if op == "help" {
			return map[string]any{"project": project, "mode": map[bool]string{true: "native-edt", false: "guarded-source-tree"}[available["update_configuration"]], "operations": []string{"prepareSource", "compare", "differences", "merge", "updateVendor", "exportDifferences", "replayCustomizations", "status", "cancel", "cleanupSources"}, "provider_support_metadata": available["update_configuration"], "warning": "guarded-source-tree mode does not transfer 1C vendor-support metadata"}, nil
		}
		if available["update_configuration"] {
			mutating := op == "merge" || op == "updateVendor" || op == "cleanupSources"
			if mutating {
				dryRun := true
				if supplied, ok := args["dryRun"].(bool); ok {
					dryRun = supplied
				}
				if !boolValue(args["confirm"]) {
					dryRun = true
				}
				call["dryRun"] = dryRun
			}
			call["projectName"] = project
			call["operation"] = op
			result, err := remote.CallTool(ctx, "update_configuration", call)
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		}
		switch op {
		case "prepareSource":
			return manager.PrepareSource(ctx, requiredString(call, "source"))
		case "compare":
			root, err := resolveEDTProjectRoot(ctx, remote, project)
			if err != nil {
				return nil, err
			}
			plan, err := manager.Compare(root, requiredString(call, "source"), requiredString(call, "ancestor"))
			if err != nil {
				return nil, err
			}
			return plan, nil
		case "differences":
			return manager.Differences(requiredString(call, "planId"), requiredString(call, "scope"), intValue(call["offset"], 0), intValue(call["limit"], 100))
		case "merge", "replayCustomizations":
			rules, err := decodeRules(call["rules"])
			if err != nil {
				return nil, err
			}
			dry := true
			if value, ok := args["dryRun"].(bool); ok {
				dry = value
			}
			return manager.Merge(requiredString(call, "planId"), rules, boolValue(call["acceptAll"]), dry, boolValue(args["confirm"]))
		case "updateVendor":
			if _, err := manager.PrepareSource(ctx, requiredString(call, "source")); err != nil {
				return nil, err
			}
			root, err := resolveEDTProjectRoot(ctx, remote, project)
			if err != nil {
				return nil, err
			}
			plan, err := manager.Compare(root, requiredString(call, "source"), requiredString(call, "ancestor"))
			if err != nil {
				return nil, err
			}
			if !boolValue(call["acceptAll"]) {
				return map[string]any{"state": "awaiting_rules", "plan": plan, "message": "review differences and call merge with explicit rules, or repeat updateVendor with acceptAll=true"}, nil
			}
			dry := true
			if value, ok := args["dryRun"].(bool); ok {
				dry = value
			}
			return manager.Merge(plan.ID, nil, boolValue(call["acceptAll"]), dry, boolValue(args["confirm"]))
		case "exportDifferences":
			return manager.ExportDifferences(requiredString(call, "planId"))
		case "status":
			return manager.Status(), nil
		case "cancel":
			return manager.Cancel(), nil
		case "cleanupSources":
			if !boolValue(args["confirm"]) {
				return nil, errors.New("cleanupSources requires confirm=true")
			}
			return manager.Cleanup()
		default:
			return nil, errors.New("unknown update operation")
		}
	}}
}

func decodeRules(value any) ([]configmerge.Rule, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("rules must be an array")
	}
	result := make([]configmerge.Rule, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("each rule must be an object")
		}
		result = append(result, configmerge.Rule{Path: requiredString(item, "path"), FQN: requiredString(item, "fqn"), Rule: requiredString(item, "rule")})
	}
	return result, nil
}
func stringArray(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed, nil
		}
		return nil, errors.New("value must be an array of strings")
	}
	result := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(string)
		if !ok {
			return nil, errors.New("value must be an array of strings")
		}
		result = append(result, item)
	}
	return result, nil
}
func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return 0
	}
}
func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return fallback
	}
}
func summarizeEventLog(value any) map[string]any {
	summary := map[string]int{}
	count := 0
	var walk func(any)
	walk = func(raw any) {
		switch typed := raw.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			count++
			level := fmt.Sprint(typed["level"])
			if level == "<nil>" || level == "" {
				level = "unknown"
			}
			summary[level]++
		}
	}
	walk(value)
	return map[string]any{"records": count, "by_level": summary, "generated_at": time.Now().UTC()}
}
