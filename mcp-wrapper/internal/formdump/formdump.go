package formdump

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

type Form struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Attributes []string `json:"attributes,omitempty"`
	Commands   []string `json:"commands,omitempty"`
	Elements   []string `json:"elements,omitempty"`
}

type Structure struct {
	Source     string `json:"source"`
	ObjectType string `json:"object_type"`
	ObjectName string `json:"object_name"`
	Forms      []Form `json:"forms"`
}

var collectionByType = map[string]string{
	"catalog": "Catalogs", "document": "Documents", "enum": "Enums",
	"report": "Reports", "dataprocessor": "DataProcessors",
	"informationregister": "InformationRegisters", "accumulationregister": "AccumulationRegisters",
	"accountingregister": "AccountingRegisters", "calculationregister": "CalculationRegisters",
	"chartofaccounts": "ChartsOfAccounts", "exchangeplan": "ExchangePlans",
	"businessprocess": "BusinessProcesses", "task": "Tasks",
}

func Read(root, objectType, objectName string) (Structure, error) {
	collection := collectionByType[strings.ToLower(strings.TrimSpace(objectType))]
	if collection == "" {
		return Structure{}, fmt.Errorf("unsupported object type %q", objectType)
	}
	formsRoot := filepath.Join(root, collection, objectName, "Forms")
	entries, err := os.ReadDir(formsRoot)
	if err != nil {
		return Structure{}, err
	}
	result := Structure{Source: "dump", ObjectType: objectType, ObjectName: objectName}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".xml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		form := Form{Name: name, Path: filepath.ToSlash(filepath.Join(collection, objectName, "Forms", entry.Name()))}
		layoutPath := filepath.Join(formsRoot, name, "Ext", "Form.xml")
		if info, err := os.Stat(layoutPath); err == nil && info.Mode().IsRegular() {
			form.Attributes, form.Commands, form.Elements, _ = parseLayout(layoutPath)
		}
		result.Forms = append(result.Forms, form)
	}
	if len(result.Forms) == 0 {
		return Structure{}, errors.New("forms not found in dump")
	}
	sort.Slice(result.Forms, func(i, j int) bool { return result.Forms[i].Name < result.Forms[j].Name })
	return result, nil
}

func parseLayout(path string) ([]string, []string, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	var stack []string
	var attributes, commands, elements []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
			name := attribute(value.Attr, "name")
			if name == "" {
				continue
			}
			switch {
			case contains(stack, "Attributes") && value.Name.Local == "Attribute":
				attributes = append(attributes, name)
			case contains(stack, "Commands") && value.Name.Local == "Command":
				commands = append(commands, name)
			case contains(stack, "ChildItems"):
				elements = append(elements, name)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return unique(attributes), unique(commands), unique(elements), nil
}

func attribute(values []xml.Attr, name string) string {
	for _, value := range values {
		if strings.EqualFold(value.Name.Local, name) {
			return value.Value
		}
	}
	return ""
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
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
	sort.Strings(result)
	return result
}
