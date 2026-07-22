package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcp-1c-analog/extension"
	"mcp-1c-analog/internal/analysis"
	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/dump"
	"mcp-1c-analog/internal/edt"
	"mcp-1c-analog/internal/installer"
	"mcp-1c-analog/internal/onec"
	"mcp-1c-analog/internal/profile"
)

func main() {
	if len(os.Args) < 2 {
		legacyMain()
		return
	}
	var err error
	switch os.Args[1] {
	case "setup":
		err = runSetup(os.Args[2:], os.Stdin, os.Stdout)
	case "profile":
		err = runProfile(os.Args[2:], os.Stdout)
	case "serve":
		err = runServe(os.Args[2:])
	case "index":
		err = runIndex(os.Args[2:], os.Stdout)
	case "analyze":
		err = runAnalyze(os.Args[2:], os.Stdout)
	default:
		legacyMain()
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runSetup(args []string, input io.Reader, output io.Writer) error {
	set := flag.NewFlagSet("setup", flag.ContinueOnError)
	set.SetOutput(output)
	id := set.String("id", "", "Profile id")
	displayName := set.String("name", "", "Profile display name")
	baseKind := set.String("base-kind", "file", "file or server")
	infobase := set.String("infobase", "", "File path or server\\infobase")
	platform := set.String("platform", "", "Exact 1cv8.exe path")
	baseURL := set.String("base-url", "http://localhost:8080/hs/mcp-1c", "Published 1C HTTP service URL")
	dumpDir := set.String("dump", "", "DumpConfigToFiles directory")
	comparisonDump := set.String("comparison-dump", "", "Fixed second dump for comparisons")
	edtWorkspace := set.String("edt-workspace", "", "EDT workspace")
	edtBridge := set.String("edt-bridge", "", "EDT bridge descriptor")
	ditrixURL := set.String("ditrix-url", "", "DitriX MCP URL")
	ditrixProject := set.String("ditrix-project", "", "Fixed EDT project")
	externalRoot := set.String("external-objects-root", "", "Managed external objects root")
	gitRoot := set.String("git-root", "", "Fixed Git repository for the scoped git tool")
	gitExecutable := set.String("git-executable", "", "Exact Git executable")
	techlogConfig := set.String("techlog-config", "", "Exact logcfg.xml managed by diagnostics")
	techlogRoot := set.String("techlog-root", "", "Fixed technological-log output directory")
	vanessaPlatform := set.String("vanessa-platform", "", "Exact 1cv8 executable for Vanessa")
	vanessaInfobase := set.String("vanessa-infobase", "", "Fixed file infobase for Vanessa")
	vanessaRunner := set.String("vanessa-runner", "", "Exact vanessa-automation.epf path")
	vanessaFeaturesRoot := set.String("vanessa-features-root", "", "Fixed Vanessa feature root")
	vanessaStepsRoot := set.String("vanessa-steps-root", "", "Optional Vanessa step-library root")
	configurationSourceRoot := set.String("configuration-source-root", "", "Fixed root for configuration update sources")
	profilesDir := set.String("profiles-dir", "", "Profiles directory")
	codexConfig := set.String("codex-config", defaultCodexConfig(), "Codex config.toml path")
	skipCodex := set.Bool("skip-codex", false, "Do not update Codex config")
	skipExtension := set.Bool("skip-extension", false, "Do not install the embedded extension")
	httpUserEnv := set.String("http-user-env", "", "Environment variable containing HTTP user")
	httpPasswordEnv := set.String("http-password-env", "", "Environment variable containing HTTP password")
	dbUserEnv := set.String("db-user-env", "ONEC_DB_USER", "Environment variable containing infobase user")
	dbPasswordEnv := set.String("db-password-env", "ONEC_DB_PASSWORD", "Environment variable containing infobase password")
	requestTimeout := set.String("request-timeout", "5m", "Live HTTP timeout")
	maxResponse := set.String("max-response-size", "128MiB", "Maximum live response")
	if err := set.Parse(args); err != nil {
		return err
	}

	reader := bufio.NewReader(input)
	if *id == "" {
		*id = prompt(reader, output, "Profile id", "onec_base")
	}
	if *displayName == "" {
		*displayName = prompt(reader, output, "Display name", *id)
	}
	if *infobase == "" {
		*infobase = prompt(reader, output, "Infobase path or server\\base", "")
	}
	if *platform == "" {
		platforms := installer.DiscoverPlatforms()
		if len(platforms) > 0 {
			*platform = platforms[0]
		}
	}
	if *edtBridge == "" {
		*edtBridge = discoverEDTBridge()
	}
	if *ditrixURL == "" && *ditrixProject != "" {
		*ditrixURL = "http://127.0.0.1:8765/mcp"
	}
	maximum, err := parseByteSize(*maxResponse)
	if err != nil {
		return fmt.Errorf("max-response-size: %w", err)
	}
	if _, err := parseTimeout(*requestTimeout); err != nil {
		return fmt.Errorf("request-timeout: %w", err)
	}
	store, err := profile.NewStore(*profilesDir)
	if err != nil {
		return err
	}
	workRoot := filepath.Join(filepath.Dir(store.Root), "work", *id)
	platformVersion := ""
	if *platform != "" {
		platformVersion = installer.PlatformVersion(*platform)
	}
	value := profile.Profile{
		ID: *id, DisplayName: *displayName, BaseKind: *baseKind, Infobase: *infobase,
		Platform: *platform, PlatformVersion: platformVersion, BaseURL: strings.TrimRight(*baseURL, "/"),
		HTTPUserEnv: *httpUserEnv, HTTPPasswordEnv: *httpPasswordEnv, DBUserEnv: *dbUserEnv, DBPasswordEnv: *dbPasswordEnv,
		DumpDir: *dumpDir, ComparisonDump: *comparisonDump, CacheDir: filepath.Join(workRoot, "cache"), WorkDir: filepath.Join(workRoot, "metadata"),
		EDTWorkspace: *edtWorkspace, EDTBridge: *edtBridge, DitrixURL: *ditrixURL, DitrixProject: *ditrixProject,
		ExternalObjectsRoot: *externalRoot, GitRoot: *gitRoot, GitExecutable: *gitExecutable,
		TechlogConfig: *techlogConfig, TechlogRoot: *techlogRoot,
		VanessaPlatform: *vanessaPlatform, VanessaInfobase: *vanessaInfobase, VanessaRunner: *vanessaRunner,
		VanessaFeaturesRoot: *vanessaFeaturesRoot, VanessaStepsRoot: *vanessaStepsRoot,
		ConfigurationSourceRoot: *configurationSourceRoot,
		RequestTimeout:          *requestTimeout, MaxResponseSize: maximum,
	}
	if !*skipExtension && value.Infobase != "" {
		if value.Platform == "" {
			return errors.New("1C platform was not found; use --platform or --skip-extension")
		}
		if err := installer.InstallWithOptions(context.Background(), extension.SourceFS, installer.Options{
			Platform: value.Platform, Infobase: value.Infobase, Server: strings.EqualFold(value.BaseKind, "server"),
			User: os.Getenv(value.DBUserEnv), Password: os.Getenv(value.DBPasswordEnv),
		}); err != nil {
			return fmt.Errorf("install extension: %w", err)
		}
		fmt.Fprintln(output, "Embedded extension installed.")
	}
	if err := store.Save(value); err != nil {
		return err
	}
	value, _ = store.Load(value.ID)
	if !*skipCodex {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		backup, err := profile.UpdateCodexConfig(*codexConfig, executable, value)
		if err != nil {
			return err
		}
		if backup != "" {
			fmt.Fprintln(output, "Codex config backup:", backup)
		}
	}
	fmt.Fprintln(output, "Profile saved:", filepath.Join(store.Root, value.ID+".json"))
	printProfileCheck(output, checkProfile(value))
	return nil
}

func runProfile(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("profile command is required: list, check, export, import or remove")
	}
	command := args[0]
	set := flag.NewFlagSet("profile "+command, flag.ContinueOnError)
	set.SetOutput(output)
	profilesDir := set.String("profiles-dir", "", "Profiles directory")
	codexConfig := set.String("codex-config", defaultCodexConfig(), "Codex config path")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	store, err := profile.NewStore(*profilesDir)
	if err != nil {
		return err
	}
	positionals := set.Args()
	switch command {
	case "list":
		values, err := store.List()
		if err != nil {
			return err
		}
		return writeJSON(output, values)
	case "check":
		if len(positionals) != 1 {
			return errors.New("usage: profile check <id>")
		}
		value, err := store.Load(positionals[0])
		if err != nil {
			return err
		}
		return writeJSON(output, checkProfile(value))
	case "export":
		if len(positionals) != 2 {
			return errors.New("usage: profile export <id> <file>")
		}
		return store.Export(positionals[0], positionals[1])
	case "import":
		if len(positionals) != 1 {
			return errors.New("usage: profile import <file>")
		}
		value, err := store.Import(positionals[0])
		if err != nil {
			return err
		}
		return writeJSON(output, value)
	case "remove":
		if len(positionals) != 1 {
			return errors.New("usage: profile remove <id>")
		}
		_ = profile.RemoveCodexProfile(*codexConfig, positionals[0])
		return store.Remove(positionals[0])
	default:
		return fmt.Errorf("unknown profile command %q", command)
	}
}

func runServe(args []string) error {
	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	profileID := set.String("profile", "", "Profile id")
	mode := set.String("mode", "db", "db or edt")
	profilesDir := set.String("profiles-dir", "", "Profiles directory")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *profileID == "" {
		return errors.New("--profile is required")
	}
	store, err := profile.NewStore(*profilesDir)
	if err != nil {
		return err
	}
	value, err := store.Load(*profileID)
	if err != nil {
		return err
	}
	legacyArgs := []string{"--cache-dir", value.CacheDir, "--work-dir", value.WorkDir,
		"--request-timeout", value.RequestTimeout, "--max-response-size", fmt.Sprint(value.MaxResponseSize)}
	if value.GitRoot != "" {
		legacyArgs = append(legacyArgs, "--git-root", value.GitRoot)
		if value.GitExecutable != "" {
			legacyArgs = append(legacyArgs, "--git-executable", value.GitExecutable)
		}
	}
	if strings.EqualFold(*mode, "db") {
		if value.BaseURL != "" {
			legacyArgs = append(legacyArgs, "--base", value.BaseURL)
		}
		if value.DumpDir != "" {
			legacyArgs = append(legacyArgs, "--dump", value.DumpDir)
		}
		if value.ComparisonDump != "" {
			legacyArgs = append(legacyArgs, "--comparison-dump", value.ComparisonDump)
		}
		if value.BaseKind == "file" && value.Platform != "" && value.Infobase != "" {
			legacyArgs = append(legacyArgs, "--platform", value.Platform, "--infobase", value.Infobase)
		}
		setCredentialEnvironment(value)
	} else if strings.EqualFold(*mode, "edt") {
		if value.EDTBridge != "" {
			legacyArgs = append(legacyArgs, "--edt-bridge", value.EDTBridge)
		}
		if value.DitrixURL != "" && value.DitrixProject != "" {
			legacyArgs = append(legacyArgs, "--ditrix-edt-url", value.DitrixURL, "--ditrix-project", value.DitrixProject)
		}
		if value.ExternalObjectsRoot != "" {
			legacyArgs = append(legacyArgs, "--external-objects-root", value.ExternalObjectsRoot)
		}
		if value.DumpDir != "" {
			legacyArgs = append(legacyArgs, "--dump", value.DumpDir)
		}
		if value.ComparisonDump != "" {
			legacyArgs = append(legacyArgs, "--comparison-dump", value.ComparisonDump)
		}
		for _, item := range []struct{ name, value string }{
			{"--techlog-config", value.TechlogConfig}, {"--techlog-root", value.TechlogRoot},
			{"--vanessa-platform", value.VanessaPlatform}, {"--vanessa-infobase", value.VanessaInfobase},
			{"--vanessa-runner", value.VanessaRunner}, {"--vanessa-features-root", value.VanessaFeaturesRoot},
			{"--vanessa-steps-root", value.VanessaStepsRoot}, {"--configuration-source-root", value.ConfigurationSourceRoot},
		} {
			if item.value != "" {
				legacyArgs = append(legacyArgs, item.name, item.value)
			}
		}
	} else {
		return errors.New("--mode must be db or edt")
	}
	os.Args = append([]string{os.Args[0]}, legacyArgs...)
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	legacyMain()
	return nil
}

func runIndex(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "build" {
		return errors.New("usage: index build --profile <id>")
	}
	set := flag.NewFlagSet("index build", flag.ContinueOnError)
	profileID := set.String("profile", "", "Profile id")
	profilesDir := set.String("profiles-dir", "", "Profiles directory")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	store, err := profile.NewStore(*profilesDir)
	if err != nil {
		return err
	}
	value, err := store.Load(*profileID)
	if err != nil {
		return err
	}
	if value.DumpDir == "" {
		return errors.New("profile has no dump directory")
	}
	index, err := dump.Open(value.DumpDir, value.CacheDir, true)
	if err != nil {
		return err
	}
	return writeJSON(output, map[string]any{"documents": index.Count(), "cache_dir": value.CacheDir})
}

func runAnalyze(args []string, output io.Writer) error {
	set := flag.NewFlagSet("analyze", flag.ContinueOnError)
	profileID := set.String("profile", "", "Profile id")
	profilesDir := set.String("profiles-dir", "", "Profiles directory")
	format := set.String("format", "json", "json, sarif or html")
	if err := set.Parse(args); err != nil {
		return err
	}
	store, err := profile.NewStore(*profilesDir)
	if err != nil {
		return err
	}
	value, err := store.Load(*profileID)
	if err != nil {
		return err
	}
	if value.DumpDir == "" {
		return errors.New("profile has no dump directory")
	}
	report, err := analysis.AnalyzeDump(value.DumpDir)
	if err != nil {
		return err
	}
	switch strings.ToLower(*format) {
	case "json":
		return writeJSON(output, report)
	case "sarif":
		return writeJSON(output, sarifReport(report))
	case "html":
		return writeHTML(output, report)
	default:
		return errors.New("format must be json, sarif or html")
	}
}

type profileCheck struct {
	ID          string          `json:"id"`
	Checks      map[string]bool `json:"checks"`
	Warnings    []string        `json:"warnings,omitempty"`
	LiveVersion any             `json:"live_version,omitempty"`
}

func checkProfile(value profile.Profile) profileCheck {
	result := profileCheck{ID: value.ID, Checks: map[string]bool{}}
	result.Checks["platform"] = regularFile(value.Platform)
	if value.BaseKind == "file" {
		result.Checks["infobase"] = directory(value.Infobase)
	} else {
		result.Checks["infobase"] = strings.TrimSpace(value.Infobase) != ""
	}
	result.Checks["dump"] = value.DumpDir == "" || directory(value.DumpDir)
	result.Checks["comparison_dump"] = value.ComparisonDump == "" || directory(value.ComparisonDump)
	result.Checks["edt_bridge"] = value.EDTBridge == "" || regularFile(value.EDTBridge)
	result.Checks["ditrix_url"] = value.DitrixURL == "" || validLoopbackMCP(value.DitrixURL)
	result.Checks["git_root"] = value.GitRoot == "" || directory(filepath.Join(value.GitRoot, ".git")) || regularFile(filepath.Join(value.GitRoot, ".git"))
	result.Checks["git_executable"] = value.GitExecutable == "" || regularFile(value.GitExecutable)
	result.Checks["techlog_config_parent"] = value.TechlogConfig == "" || directory(filepath.Dir(value.TechlogConfig))
	result.Checks["techlog_root"] = value.TechlogRoot == "" || directory(value.TechlogRoot) || directory(filepath.Dir(value.TechlogRoot))
	result.Checks["vanessa_platform"] = value.VanessaPlatform == "" || regularFile(value.VanessaPlatform)
	result.Checks["vanessa_infobase"] = value.VanessaInfobase == "" || directory(value.VanessaInfobase)
	result.Checks["vanessa_runner"] = value.VanessaRunner == "" || regularFile(value.VanessaRunner)
	result.Checks["vanessa_features_root"] = value.VanessaFeaturesRoot == "" || directory(value.VanessaFeaturesRoot)
	result.Checks["vanessa_steps_root"] = value.VanessaStepsRoot == "" || directory(value.VanessaStepsRoot)
	result.Checks["configuration_source_root"] = value.ConfigurationSourceRoot == "" || directory(value.ConfigurationSourceRoot)
	if value.EDTBridge != "" && result.Checks["edt_bridge"] {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := edt.New(value.EDTBridge).Health(ctx)
		cancel()
		result.Checks["edt_health"] = err == nil
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
		}
	}
	if value.DitrixURL != "" && result.Checks["ditrix_url"] {
		client, err := ditrix.New(value.DitrixURL)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err = client.Initialize(ctx)
			cancel()
		}
		result.Checks["ditrix_health"] = err == nil
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
		}
	}
	if value.BaseURL != "" {
		timeout, _ := time.ParseDuration(value.RequestTimeout)
		client := onec.NewClientWithOptions(value.BaseURL, envValue(value.HTTPUserEnv), envValue(value.HTTPPasswordEnv), timeout, value.MaxResponseSize)
		var version any
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Get(ctx, "/version", &version)
		cancel()
		if err != nil {
			result.Checks["live_http"] = false
			result.Warnings = append(result.Warnings, err.Error())
		} else {
			result.Checks["live_http"] = true
			result.LiveVersion = version
		}
	}
	return result
}

func setCredentialEnvironment(value profile.Profile) {
	if value.HTTPUserEnv != "" {
		_ = os.Setenv("MCP_1C_USER", os.Getenv(value.HTTPUserEnv))
	}
	if value.HTTPPasswordEnv != "" {
		_ = os.Setenv("MCP_1C_PASSWORD", os.Getenv(value.HTTPPasswordEnv))
	}
	if value.DBUserEnv != "" {
		_ = os.Setenv("ONEC_DB_USER", os.Getenv(value.DBUserEnv))
	}
	if value.DBPasswordEnv != "" {
		_ = os.Setenv("ONEC_DB_PASSWORD", os.Getenv(value.DBPasswordEnv))
	}
}

func prompt(reader *bufio.Reader, output io.Writer, label, fallback string) string {
	if fallback != "" {
		fmt.Fprintf(output, "%s [%s]: ", label, fallback)
	} else {
		fmt.Fprintf(output, "%s: ", label)
	}
	value, _ := reader.ReadString('\n')
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func discoverEDTBridge() string {
	for _, candidate := range []string{os.Getenv("ONEC_MCP_EDT_BRIDGE"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "mcp-1c-edt", "bridge.json"),
		filepath.Join(os.Getenv("USERPROFILE"), ".onec-edt-mcp", "bridge.json")} {
		if regularFile(candidate) {
			return candidate
		}
	}
	return ""
}

func defaultCodexConfig() string {
	return filepath.Join(os.Getenv("USERPROFILE"), ".codex", "config.toml")
}

func regularFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func directory(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func validLoopbackMCP(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Path != "/mcp" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func envValue(name string) string {
	if name == "" {
		return ""
	}
	return os.Getenv(name)
}

func printProfileCheck(output io.Writer, result profileCheck) { _ = writeJSON(output, result) }

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func sarifReport(report analysis.Report) map[string]any {
	var results []map[string]any
	for _, diagnostic := range report.Diagnostics {
		level := diagnostic.Severity
		if level == "warning" {
			level = "warning"
		} else {
			level = "error"
		}
		results = append(results, map[string]any{
			"ruleId": diagnostic.Rule, "level": level, "message": map[string]any{"text": diagnostic.Message},
			"locations": []any{map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]any{"uri": diagnostic.Path}, "region": map[string]any{"startLine": max(1, diagnostic.Line)}}}},
		})
	}
	return map[string]any{"version": "2.1.0", "$schema": "https://json.schemastore.org/sarif-2.1.0.json", "runs": []any{map[string]any{"tool": map[string]any{"driver": map[string]any{"name": "mcp-1c-analog"}}, "results": results}}}
}

func writeHTML(output io.Writer, report analysis.Report) error {
	page := template.Must(template.New("report").Parse(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>mcp-1c-analog analysis</title><style>body{font:14px system-ui;margin:2rem;max-width:1100px}table{border-collapse:collapse;width:100%}th,td{padding:.5rem;border-bottom:1px solid #ddd;text-align:left}.error{color:#a00}.warning{color:#a60}code{font-family:ui-monospace}</style><h1>Анализ конфигурации 1С</h1><p>Файлов: {{.Files}} · Методов: {{len .Symbols}} · Диагностик: {{len .Diagnostics}}</p><table><tr><th>Уровень</th><th>Файл</th><th>Строка</th><th>Правило</th><th>Сообщение</th></tr>{{range .Diagnostics}}<tr><td class="{{.Severity}}">{{.Severity}}</td><td><code>{{.Path}}</code></td><td>{{.Line}}</td><td>{{.Rule}}</td><td>{{.Message}}</td></tr>{{end}}</table></html>`))
	return page.Execute(output, report)
}
