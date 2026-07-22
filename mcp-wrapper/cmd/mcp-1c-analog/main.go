package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mcp-1c-analog/extension"
	"mcp-1c-analog/internal/bslhelp"
	"mcp-1c-analog/internal/designer"
	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/dump"
	"mcp-1c-analog/internal/edt"
	"mcp-1c-analog/internal/installer"
	"mcp-1c-analog/internal/mcp"
	"mcp-1c-analog/internal/metadata"
	"mcp-1c-analog/internal/onec"
	"mcp-1c-analog/internal/tools"
)

var version = "dev"

func legacyMain() {
	baseURL := flag.String("base", "http://localhost:8080/hs/mcp-1c", "Base URL of the 1C HTTP service")
	user := flag.String("user", "", "1C HTTP service user")
	password := flag.String("password", "", "1C HTTP service password")
	dumpDir := flag.String("dump", "", "Path to DumpConfigToFiles output; enables search_code")
	comparisonDump := flag.String("comparison-dump", "", "Fixed second DumpConfigToFiles directory for comparison tools")
	cacheDir := flag.String("cache-dir", "", "Directory for cache and logs")
	reindex := flag.Bool("reindex", false, "Force rebuild of the dump search index")
	installDB := flag.String("install", "", "Install extension into a file infobase")
	installServer := flag.Bool("server", false, "Treat --install or --infobase as a server infobase connection string")
	platform := flag.String("platform", "", "Path to 1cv8 executable")
	infobase := flag.String("infobase", "", "Locked file infobase for metadata management tools")
	workDir := flag.String("work-dir", "", "Directory for prepared plans, canonical dumps, backups and Designer logs")
	edtBridge := flag.String("edt-bridge", "", "Fixed path to the EDT bridge.json descriptor; enables direct EDT metadata tools")
	ditrixEDTURL := flag.String("ditrix-edt-url", "", "Fixed loopback URL of a separately installed DitriX EDT-MCP server")
	ditrixProject := flag.String("ditrix-project", "", "Fixed EDT project exposed through DitriX EDT-MCP")
	externalObjectsRoot := flag.String("external-objects-root", "", "Existing root allowed for managed external-object XML sources and builds")
	gitRoot := flag.String("git-root", "", "Fixed Git repository exposed through the scoped git tool")
	gitExecutable := flag.String("git-executable", "", "Exact Git executable; defaults to PATH lookup")
	bslLanguageServer := flag.String("bsl-language-server", "", "Exact BSL Language Server executable or JAR; enables code_review")
	javaExecutable := flag.String("java-executable", "", "Exact Java executable used when --bsl-language-server points to a JAR")
	bslLanguageServerConfig := flag.String("bsl-language-server-config", "", "Fixed BSL Language Server configuration file")
	debug := flag.Bool("debug", false, "Write debug logs to cache-dir/server.log")
	requestTimeout := flag.Duration("request-timeout", onec.DefaultRequestTimeout, "Timeout for a live 1C HTTP request")
	maxResponseSize := flag.Int64("max-response-size", onec.DefaultMaxResponseBytes, "Maximum live 1C JSON response size in bytes")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("mcp-1c-analog " + version)
		return
	}

	if *cacheDir == "" {
		*cacheDir = defaultCacheDir()
	}
	if *workDir == "" {
		*workDir = filepath.Join(*cacheDir, "metadata-work")
	}
	dbUser := os.Getenv("ONEC_DB_USER")
	dbPassword := os.Getenv("ONEC_DB_PASSWORD")
	if *user == "" {
		*user = os.Getenv("MCP_1C_USER")
	}
	if *password == "" {
		*password = os.Getenv("MCP_1C_PASSWORD")
	}
	if *debug {
		if err := enableFileLog(*cacheDir); err != nil {
			fmt.Fprintf(os.Stderr, "debug log disabled: %v\n", err)
		}
	} else {
		log.SetOutput(os.Stderr)
	}

	if *installDB != "" {
		if err := installer.InstallWithOptions(context.Background(), extension.SourceFS, installer.Options{Platform: *platform, Infobase: *installDB, Server: *installServer, User: dbUser, Password: dbPassword}); err != nil {
			fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Extension installed successfully.")
		return
	}

	client := onec.NewClientWithOptions(*baseURL, *user, *password, *requestTimeout, *maxResponseSize)
	var index *dump.Index
	if *dumpDir != "" {
		var err error
		index, err = dump.Open(*dumpDir, *cacheDir, *reindex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot open dump index: %v\n", err)
			os.Exit(1)
		}
	}

	if (*ditrixEDTURL == "") != (*ditrixProject == "") {
		fmt.Fprintln(os.Stderr, "--ditrix-edt-url and --ditrix-project must be specified together")
		os.Exit(2)
	}
	var proxyClient *ditrix.Client
	if *ditrixEDTURL != "" {
		var err error
		proxyClient, err = ditrix.New(*ditrixEDTURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "DitriX EDT-MCP initialization failed: %v\n", err)
			os.Exit(1)
		}
	}

	server := mcp.NewServer("mcp-1c-analog", version)
	server.SetInstructions("This server is locked to one configured 1C target. Never request or expose credentials or tokens. For BSL work, inspect existing project code and use EDT content assist, platform documentation, symbol navigation, call hierarchy, diagnostics and code_review instead of guessing platform syntax. DitriX EDT tools are proxied only for the configured project. launch_debugger execution controls, edit_metadata mutations and manage_infobase mutations require confirm=true; prefer their help/read/dry-run operations first. write_module_source is dry-run by default; review its preview before setting dryRun=false. Exports stay below the configured work directory. The git tool is limited to one configured repository, disables hooks and requires confirm=true for mutations. Managed external-object tools are additionally limited to CodexExt_* projects and paths below the configured external root; they never update the infobase. To clone metadata, always call prepare_clone_metadata first, review its plan_id and summary, then call apply_prepared_change only after explicit user approval. Never guess or reuse a plan_id. A prepared plan is refused if the source project or configuration changed.")
	tools.RegisterWithOptions(server, client, index, bslhelp.Default(), tools.RegisterOptions{
		DumpDir: *dumpDir, DitrixClient: proxyClient, DitrixProject: *ditrixProject,
	})
	if *edtBridge != "" && (*platform != "" || *infobase != "") {
		fmt.Fprintln(os.Stderr, "--edt-bridge cannot be combined with --platform or --infobase")
		os.Exit(2)
	}
	var edtClient *edt.Client
	if *edtBridge != "" {
		edtClient = edt.New(*edtBridge)
		tools.RegisterEdtMetadata(server, edtClient)
		tools.RegisterInfobaseManagement(server, edtClient)
	}
	if proxyClient != nil {
		report, err := tools.RegisterDitrixEDTWithOptions(context.Background(), server, proxyClient, *ditrixProject,
			tools.DitrixRegistrationOptions{WorkDir: *workDir, BSLLanguageServer: *bslLanguageServer,
				JavaExecutable: *javaExecutable, BSLLanguageServerConfig: *bslLanguageServerConfig})
		if err != nil {
			fmt.Fprintf(os.Stderr, "DitriX EDT-MCP discovery failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "DitriX EDT-MCP %s: registered %d tools for fixed project %q; kept %d native tools; excluded %d by policy\n", report.ServerVersion, len(report.Registered), report.LockedProject, len(report.NativeWins), len(report.PolicyExcluded))
	}
	if *externalObjectsRoot != "" {
		if edtClient == nil || proxyClient == nil {
			fmt.Fprintln(os.Stderr, "--external-objects-root requires --edt-bridge, --ditrix-edt-url and --ditrix-project")
			os.Exit(2)
		}
		if err := tools.RegisterManagedExternalObjects(server, edtClient, proxyClient, *externalObjectsRoot); err != nil {
			fmt.Fprintf(os.Stderr, "managed external-object tools initialization failed: %v\n", err)
			os.Exit(1)
		}
	}
	var designerClient *designer.Client
	if *platform != "" || *infobase != "" {
		if *platform == "" || *infobase == "" {
			fmt.Fprintln(os.Stderr, "--platform and --infobase must be specified together")
			os.Exit(2)
		}
		designerClient = designer.New(*platform, *infobase, dbUser, dbPassword, *workDir)
		if err := designerClient.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "metadata management initialization failed: %v\n", err)
			os.Exit(1)
		}
		tools.RegisterMetadata(server, metadata.NewManager(designerClient, *workDir))
	}
	if proxyClient != nil || designerClient != nil {
		if err := tools.RegisterExportObject(server, proxyClient, designerClient, *ditrixProject, filepath.Join(*workDir, "exports")); err != nil {
			fmt.Fprintf(os.Stderr, "export_object initialization failed: %v\n", err)
			os.Exit(1)
		}
	}
	tools.RegisterAnalysis(server, index, *dumpDir, *comparisonDump)
	tools.RegisterWorkspace(server, *workDir)
	if *gitRoot != "" {
		if err := tools.RegisterGit(server, tools.GitOptions{Root: *gitRoot, Executable: *gitExecutable, WorkDir: *workDir}); err != nil {
			fmt.Fprintf(os.Stderr, "git tool initialization failed: %v\n", err)
			os.Exit(1)
		}
	}
	if err := server.ServeOfficialStdio(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "mcp server failed: %v\n", err)
		os.Exit(1)
	}
}

func parseByteSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	multiplier := int64(1)
	for suffix, size := range map[string]int64{"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "kb": 1000, "mb": 1000 * 1000, "gb": 1000 * 1000 * 1000} {
		if strings.HasSuffix(value, suffix) {
			multiplier = size
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
			break
		}
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid byte size")
	}
	return number * multiplier, nil
}

func parseTimeout(value string) (time.Duration, error) { return time.ParseDuration(value) }

func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ".mcp-1c-analog-cache"
	}
	return filepath.Join(base, "mcp-1c-analog")
}

func enableFileLog(cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(cacheDir, "server.log"))
	if err != nil {
		return err
	}
	log.SetOutput(f)
	return nil
}
