package metadata

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var configVersionPattern = regexp.MustCompile(`\s+configVersion="[^"]*"`)

type CloneResult struct {
	Source       Object            `json:"source"`
	Target       Object            `json:"target"`
	UUIDMap      map[string]string `json:"uuid_map"`
	ChangedFiles []string          `json:"changed_files"`
}

func ValidIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range []rune(value) {
		if index == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func Clone(dumpDir, typeName, sourceName, targetName string) (CloneResult, error) {
	if !ValidIdentifier(typeName) || !ValidIdentifier(sourceName) || !ValidIdentifier(targetName) {
		return CloneResult{}, fmt.Errorf("metadata type and names must be valid 1C identifiers")
	}
	source, err := Find(dumpDir, typeName, sourceName)
	if err != nil {
		return CloneResult{}, err
	}
	if _, err := Find(dumpDir, typeName, targetName); err == nil {
		return CloneResult{}, fmt.Errorf("metadata object %s.%s already exists", typeName, targetName)
	}
	targetXML := filepath.Join(filepath.Dir(source.XMLPath), targetName+filepath.Ext(source.XMLPath))
	targetCompanion := ""
	if source.CompanionPath != "" {
		targetCompanion = filepath.Join(filepath.Dir(source.CompanionPath), targetName)
	}
	if err := copyFile(source.XMLPath, targetXML); err != nil {
		return CloneResult{}, err
	}
	if targetCompanion != "" {
		if err := copyTree(source.CompanionPath, targetCompanion); err != nil {
			return CloneResult{}, err
		}
	}
	paths := []string{targetXML}
	if targetCompanion != "" {
		err = filepath.WalkDir(targetCompanion, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".xml") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return CloneResult{}, err
		}
	}
	uuidMap, err := buildUUIDMap(paths)
	if err != nil {
		return CloneResult{}, err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return CloneResult{}, err
		}
		data = replaceUUIDs(data, uuidMap)
		data = bytes.ReplaceAll(data, []byte("."+sourceName), []byte("."+targetName))
		if path == targetXML {
			oldName := []byte("<Name>" + sourceName + "</Name>")
			newName := []byte("<Name>" + targetName + "</Name>")
			if !bytes.Contains(data, oldName) {
				return CloneResult{}, fmt.Errorf("root Name element for %s was not found", sourceName)
			}
			data = bytes.Replace(data, oldName, newName, 1)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return CloneResult{}, err
		}
	}
	configPath := filepath.Join(dumpDir, "Configuration.xml")
	if err := addToConfiguration(configPath, typeName, sourceName, targetName); err != nil {
		return CloneResult{}, err
	}
	dumpInfoPath := filepath.Join(dumpDir, "ConfigDumpInfo.xml")
	if err := cloneDumpInfo(dumpInfoPath, typeName, sourceName, targetName, uuidMap); err != nil {
		return CloneResult{}, err
	}
	target, err := Inspect(dumpDir, typeName, targetName)
	if err != nil {
		return CloneResult{}, err
	}
	result := CloneResult{Source: source, Target: target, UUIDMap: uuidMap}
	for _, path := range append(paths, configPath, dumpInfoPath) {
		rel, _ := filepath.Rel(dumpDir, path)
		result.ChangedFiles = append(result.ChangedFiles, rel)
	}
	sort.Strings(result.ChangedFiles)
	return result, nil
}

func Equivalent(dumpDir, typeName, sourceName, targetName string) error {
	source, err := Inspect(dumpDir, typeName, sourceName)
	if err != nil {
		return err
	}
	target, err := Inspect(dumpDir, typeName, targetName)
	if err != nil {
		return err
	}
	sourceFiles, err := relativeObjectFiles(source, sourceName)
	if err != nil {
		return err
	}
	targetFiles, err := relativeObjectFiles(target, targetName)
	if err != nil {
		return err
	}
	if len(sourceFiles) != len(targetFiles) {
		return fmt.Errorf("clone file count differs: source=%d target=%d", len(sourceFiles), len(targetFiles))
	}
	for rel, sourcePath := range sourceFiles {
		targetPath, ok := targetFiles[rel]
		if !ok {
			return fmt.Errorf("clone is missing file %s", rel)
		}
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		targetData, err := os.ReadFile(targetPath)
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Ext(sourcePath), ".xml") {
			sourceData = normalizeXMLIdentity(sourceData, sourceName, sourceName)
			targetData = normalizeXMLIdentity(targetData, targetName, sourceName)
		}
		if !bytes.Equal(sourceData, targetData) {
			return fmt.Errorf("clone content differs in %s", rel)
		}
	}
	return nil
}

func normalizeXMLIdentity(data []byte, currentName, normalizedName string) []byte {
	data = bytes.ReplaceAll(data, []byte("."+currentName), []byte("."+normalizedName))
	data = bytes.Replace(data, []byte("<Name>"+currentName+"</Name>"), []byte("<Name>"+normalizedName+"</Name>"), 1)
	data = uuidPattern.ReplaceAll(data, []byte("00000000-0000-0000-0000-000000000000"))
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return data
}

func relativeObjectFiles(object Object, objectName string) (map[string]string, error) {
	result := map[string]string{"__root__.xml": object.XMLPath}
	if object.CompanionPath == "" {
		return result, nil
	}
	err := filepath.WalkDir(object.CompanionPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(object.CompanionPath, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = path
		return nil
	})
	return result, err
}

func buildUUIDMap(paths []string) (map[string]string, error) {
	result := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, value := range uuidPattern.FindAllString(string(data), -1) {
			key := strings.ToLower(value)
			if _, exists := result[key]; !exists {
				id, err := newUUID()
				if err != nil {
					return nil, err
				}
				result[key] = id
			}
		}
	}
	return result, nil
}

func replaceUUIDs(data []byte, replacements map[string]string) []byte {
	return uuidPattern.ReplaceAllFunc(data, func(value []byte) []byte {
		if replacement, ok := replacements[strings.ToLower(string(value))]; ok {
			return []byte(replacement)
		}
		return value
	})
}

func newUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func addToConfiguration(path, typeName, sourceName, targetName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	entry := []byte("<" + typeName + ">" + sourceName + "</" + typeName + ">")
	if !bytes.Contains(data, entry) {
		return fmt.Errorf("source object is absent from Configuration.xml ChildObjects")
	}
	if bytes.Contains(data, []byte("<"+typeName+">"+targetName+"</"+typeName+">")) {
		return fmt.Errorf("target object is already present in Configuration.xml")
	}
	replacement := append(append([]byte{}, entry...), []byte("\n\t\t\t<"+typeName+">"+targetName+"</"+typeName+">")...)
	data = bytes.Replace(data, entry, replacement, 1)
	return os.WriteFile(path, data, 0o600)
}

func cloneDumpInfo(path, typeName, sourceName, targetName string, replacements map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	newline := "\n"
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	prefix := typeName + "." + sourceName
	var output []string
	cloned := 0
	for index := 0; index < len(lines); {
		line := lines[index]
		if !strings.HasPrefix(line, "\t\t<Metadata ") {
			output = append(output, line)
			index++
			continue
		}
		end := index
		if !strings.Contains(line, "/>") {
			depth := 1
			for depth > 0 && end+1 < len(lines) {
				end++
				trimmed := strings.TrimSpace(lines[end])
				if strings.HasPrefix(trimmed, "<Metadata ") && !strings.Contains(trimmed, "/>") {
					depth++
				}
				if strings.HasPrefix(trimmed, "</Metadata>") {
					depth--
				}
			}
		}
		segment := strings.Join(lines[index:end+1], newline)
		output = append(output, segment)
		nameMarker := `name="` + prefix
		if strings.Contains(line, nameMarker+`"`) || strings.Contains(line, nameMarker+`.`) {
			clone := strings.ReplaceAll(segment, prefix, typeName+"."+targetName)
			clone = string(replaceUUIDs([]byte(clone), replacements))
			clone = configVersionPattern.ReplaceAllString(clone, "")
			output = append(output, clone)
			cloned++
		}
		index = end + 1
	}
	if cloned == 0 {
		return fmt.Errorf("source metadata entries were not found in ConfigDumpInfo.xml")
	}
	return os.WriteFile(path, []byte(strings.Join(output, newline)), 0o600)
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		return copyFile(path, destination)
	})
}

func copyFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o600)
}
