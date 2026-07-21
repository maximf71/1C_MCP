package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Object struct {
	Type          string   `json:"type"`
	Name          string   `json:"name"`
	XMLPath       string   `json:"xml_path"`
	CompanionPath string   `json:"companion_path,omitempty"`
	Files         []string `json:"files,omitempty"`
	Size          int64    `json:"size_bytes,omitempty"`
	UUIDCount     int      `json:"uuid_count,omitempty"`
}

func Discover(dumpDir string) ([]Object, error) {
	entries, err := os.ReadDir(dumpDir)
	if err != nil {
		return nil, err
	}
	var objects []Object
	for _, collection := range entries {
		if !collection.IsDir() {
			continue
		}
		collectionPath := filepath.Join(dumpDir, collection.Name())
		files, err := os.ReadDir(collectionPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range files {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".xml") {
				continue
			}
			xmlPath := filepath.Join(collectionPath, entry.Name())
			typeName, objectName, err := readIdentity(xmlPath)
			if err != nil || typeName == "" || objectName == "" || typeName == "Configuration" {
				continue
			}
			companion := strings.TrimSuffix(xmlPath, filepath.Ext(xmlPath))
			if info, statErr := os.Stat(companion); statErr != nil || !info.IsDir() {
				companion = ""
			}
			objects = append(objects, Object{Type: typeName, Name: objectName, XMLPath: xmlPath, CompanionPath: companion})
		}
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].Type == objects[j].Type {
			return objects[i].Name < objects[j].Name
		}
		return objects[i].Type < objects[j].Type
	})
	return objects, nil
}

func Find(dumpDir, typeName, name string) (Object, error) {
	objects, err := Discover(dumpDir)
	if err != nil {
		return Object{}, err
	}
	for _, object := range objects {
		if strings.EqualFold(object.Type, typeName) && object.Name == name {
			return object, nil
		}
	}
	return Object{}, fmt.Errorf("metadata object %s.%s was not found", typeName, name)
}

func Inspect(dumpDir, typeName, name string) (Object, error) {
	object, err := Find(dumpDir, typeName, name)
	if err != nil {
		return Object{}, err
	}
	paths := []string{object.XMLPath}
	if object.CompanionPath != "" {
		err = filepath.WalkDir(object.CompanionPath, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return Object{}, err
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return Object{}, statErr
		}
		object.Size += info.Size()
		rel, relErr := filepath.Rel(dumpDir, path)
		if relErr != nil {
			return Object{}, relErr
		}
		object.Files = append(object.Files, rel)
		if strings.EqualFold(filepath.Ext(path), ".xml") {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return Object{}, readErr
			}
			object.UUIDCount += len(uuidPattern.FindAll(data, -1))
		}
	}
	return object, nil
}

func Fingerprint(dumpDir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dumpDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, path := range files {
		rel, err := filepath.Rel(dumpDir, path)
		if err != nil {
			return "", err
		}
		io.WriteString(h, filepath.ToSlash(rel))
		h.Write([]byte{0})
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readIdentity(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	depth := 0
	rootType := ""
	inRootProperties := false
	propertiesDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				rootType = value.Name.Local
			}
			if depth == 3 && value.Name.Local == "Properties" {
				inRootProperties = true
				propertiesDepth = depth
			}
			if inRootProperties && depth == propertiesDepth+1 && value.Name.Local == "Name" {
				var name string
				if err := decoder.DecodeElement(&name, &value); err != nil {
					return "", "", err
				}
				return rootType, name, nil
			}
		case xml.EndElement:
			if inRootProperties && depth == propertiesDepth {
				inRootProperties = false
			}
			depth--
		}
	}
	return rootType, "", nil
}
