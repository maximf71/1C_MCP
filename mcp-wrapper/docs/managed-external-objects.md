# Managed external objects in EDT

Configure all four options together:

```text
--edt-bridge <bridge.json>
--ditrix-edt-url http://127.0.0.1:<port>/mcp
--ditrix-project <fixed base project>
--external-objects-root <existing source root>
```

The resulting bounded workflow consists of:

- `import_external_object_xml`: create or update a `CodexExt_*` EDT external-object project from a root Designer XML file and link it to the fixed base configuration;
- `validate_external_object_project`: refresh, revalidate, and return detailed EDT diagnostics;
- `get_external_object_project_errors`: read the current diagnostics without rebuilding;
- `build_external_object_project`: build one or all objects below `.edt-external-builds/<project>` in the configured root.

XML paths are relative to the configured root. The STDIO proxy and EDT bridge independently reject absolute paths, traversal, symlink escapes, non-XML sources, and project names outside `CodexExt_*`. Builds disable the upstream build-time comment mutation. None of these tools updates the infobase.

The EDT bridge uses `IImportOperationFactory.createImportExternalObjectOperation`, so the imported project is a real EDT external-object project, not a copied directory.
