package extension

import "embed"

// SourceFS contains the 1C extension source in DumpConfigToFiles format.
//
//go:embed src/**
var SourceFS embed.FS
