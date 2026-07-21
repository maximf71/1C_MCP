package subsystems

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Forest struct {
	Subsystems []Node   `json:"subsystems"`
	AllObjects []string `json:"allObjects"`
	Warnings   []string `json:"warnings,omitempty"`
}

type Node struct {
	Name       string   `json:"name"`
	FullName   string   `json:"fullName,omitempty"`
	Content    []string `json:"content"`
	Subsystems []Node   `json:"subsystems,omitempty"`
}

type Reference struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

func ReadDump(root string) (Forest, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Forest{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return Forest{}, fmt.Errorf("invalid dump root: %w", err)
	}
	forest := Forest{AllObjects: readUniverse(absolute)}
	subsystemsRoot := filepath.Join(absolute, "Subsystems")
	entries, err := os.ReadDir(subsystemsRoot)
	if os.IsNotExist(err) {
		return forest, nil
	}
	if err != nil {
		return Forest{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".xml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		node, err := readNode(filepath.Join(subsystemsRoot, entry.Name()), filepath.Join(subsystemsRoot, name), "")
		if err != nil {
			forest.Warnings = append(forest.Warnings, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		forest.Subsystems = append(forest.Subsystems, node)
	}
	sort.Slice(forest.Subsystems, func(i, j int) bool { return forest.Subsystems[i].Name < forest.Subsystems[j].Name })
	return forest, nil
}

func readNode(xmlPath, objectDir, parent string) (Node, error) {
	name, content, children, err := parseSubsystemXML(xmlPath)
	if err != nil {
		return Node{}, err
	}
	full := name
	if parent != "" {
		full = parent + "." + name
	}
	node := Node{Name: name, FullName: full, Content: content}
	for _, child := range children {
		childPath := filepath.Join(objectDir, "Subsystems", child+".xml")
		childNode, err := readNode(childPath, filepath.Join(objectDir, "Subsystems", child), full)
		if err != nil {
			continue
		}
		node.Subsystems = append(node.Subsystems, childNode)
	}
	sort.Strings(node.Content)
	sort.Slice(node.Subsystems, func(i, j int) bool { return node.Subsystems[i].Name < node.Subsystems[j].Name })
	return node, nil
}

func parseSubsystemXML(path string) (string, []string, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, nil, err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	var name string
	var content, children []string
	var stack []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
			if value.Name.Local == "Name" && parentIs(stack, "Properties") && name == "" {
				if err := decoder.DecodeElement(&name, &value); err != nil {
					return "", nil, nil, err
				}
				stack = stack[:len(stack)-1]
			} else if value.Name.Local == "Item" && containsAncestor(stack, "Content") {
				var item string
				if err := decoder.DecodeElement(&item, &value); err == nil && strings.TrimSpace(item) != "" {
					content = append(content, strings.TrimSpace(item))
				}
				stack = stack[:len(stack)-1]
			} else if value.Name.Local == "Subsystem" && containsAncestor(stack, "ChildObjects") {
				var child string
				if err := decoder.DecodeElement(&child, &value); err == nil && strings.TrimSpace(child) != "" {
					children = append(children, strings.TrimSpace(child))
				}
				stack = stack[:len(stack)-1]
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if name == "" {
		return "", nil, nil, errors.New("subsystem name not found")
	}
	return name, unique(content), unique(children), nil
}

func parentIs(stack []string, name string) bool {
	return len(stack) >= 2 && stack[len(stack)-2] == name
}

func containsAncestor(stack []string, name string) bool {
	for _, value := range stack[:len(stack)-1] {
		if value == name {
			return true
		}
	}
	return false
}

var universeKinds = map[string]string{
	"Catalogs": "Catalog", "Documents": "Document", "Enums": "Enum",
	"Reports": "Report", "DataProcessors": "DataProcessor",
	"InformationRegisters": "InformationRegister", "AccumulationRegisters": "AccumulationRegister",
	"AccountingRegisters": "AccountingRegister", "CalculationRegisters": "CalculationRegister",
	"ChartsOfAccounts": "ChartOfAccounts", "ChartsOfCharacteristicTypes": "ChartOfCharacteristicTypes",
	"ChartsOfCalculationTypes": "ChartOfCalculationTypes", "ExchangePlans": "ExchangePlan",
	"BusinessProcesses": "BusinessProcess", "Tasks": "Task", "Constants": "Constant",
	"CommonModules": "CommonModule", "CommonForms": "CommonForm", "CommonCommands": "CommonCommand",
	"CommandGroups": "CommandGroup", "CommonTemplates": "CommonTemplate", "CommonPictures": "CommonPicture",
	"Roles": "Role", "DefinedTypes": "DefinedType", "HTTPServices": "HTTPService",
	"WebServices": "WebService", "XDTOPackages": "XDTOPackage", "SessionParameters": "SessionParameter",
	"ScheduledJobs": "ScheduledJob", "FunctionalOptions": "FunctionalOption",
	"FunctionalOptionsParameters": "FunctionalOptionsParameter", "EventSubscriptions": "EventSubscription",
	"DocumentJournals": "DocumentJournal", "FilterCriteria": "FilterCriterion",
}

func readUniverse(root string) []string {
	var result []string
	for directory, kind := range universeKinds {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".xml") {
				continue
			}
			result = append(result, kind+"."+strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		}
	}
	sort.Strings(result)
	return unique(result)
}

func Analyze(forest Forest, action, object, objectType string, crossBranchOnly bool) (map[string]any, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "orphans" && action != "containing" && action != "intersections" {
		return nil, errors.New("action must be orphans, containing or intersections")
	}
	membership := flatten(forest)
	result := map[string]any{"action": action, "warnings": forest.Warnings}
	switch action {
	case "orphans":
		var objects []string
		for _, item := range forest.AllObjects {
			if len(membership[item]) == 0 {
				objects = append(objects, item)
			}
		}
		result["objects"] = objects
		result["count"] = len(objects)
	case "containing":
		if strings.TrimSpace(object) == "" {
			return nil, errors.New("object is required for action=containing")
		}
		matches := map[string][]Reference{}
		for full, refs := range membership {
			if matchesObject(full, object, objectType) {
				matches[full] = refs
			}
		}
		result["matches"] = matches
		result["count"] = len(matches)
	case "intersections":
		matches := map[string][]Reference{}
		for full, refs := range membership {
			refs = uniqueReferences(refs)
			if len(refs) < 2 || crossBranchOnly && rootCount(refs) < 2 {
				continue
			}
			matches[full] = refs
		}
		result["matches"] = matches
		result["count"] = len(matches)
	}
	return result, nil
}

func flatten(forest Forest) map[string][]Reference {
	result := map[string][]Reference{}
	var walk func([]Node, string)
	walk = func(nodes []Node, root string) {
		for _, node := range nodes {
			currentRoot := root
			if currentRoot == "" {
				currentRoot = node.Name
			}
			for _, object := range node.Content {
				result[object] = append(result[object], Reference{Name: node.FullName, Root: currentRoot})
			}
			walk(node.Subsystems, currentRoot)
		}
	}
	walk(forest.Subsystems, "")
	for key, refs := range result {
		result[key] = uniqueReferences(refs)
	}
	return result
}

func matchesObject(full, query, objectType string) bool {
	if strings.EqualFold(full, strings.TrimSpace(query)) {
		return true
	}
	parts := strings.Split(full, ".")
	if !strings.EqualFold(parts[len(parts)-1], strings.TrimSpace(query)) {
		return false
	}
	return objectType == "" || strings.EqualFold(parts[0], strings.TrimSpace(objectType))
}

func rootCount(refs []Reference) int {
	values := map[string]bool{}
	for _, ref := range refs {
		values[ref.Root] = true
	}
	return len(values)
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func uniqueReferences(values []Reference) []Reference {
	seen := map[string]bool{}
	result := values[:0]
	for _, value := range values {
		key := value.Name + "\x00" + value.Root
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Root < result[j].Root
		}
		return result[i].Name < result[j].Name
	})
	return result
}
