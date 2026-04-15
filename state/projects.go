package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project represents a registered project directory.
type Project struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"` // project root (parent of .daggle/)
}

// DAGSource represents a directory containing DAG YAML files, with a label.
type DAGSource struct {
	Name string // project name or "global"
	Dir  string // absolute path to directory with YAML files
}

type projectsFile struct {
	Projects []Project `yaml:"projects"`
}

// ProjectsPath returns the path to the projects registry file.
func ProjectsPath() string {
	return filepath.Join(ConfigDir(), "projects.yaml")
}

// LoadProjects reads the project registry. Returns empty list if file doesn't exist.
func LoadProjects() ([]Project, error) {
	data, err := os.ReadFile(ProjectsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var f projectsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ProjectsPath(), err)
	}
	return f.Projects, nil
}

// SaveProjects writes the project registry.
func SaveProjects(projects []Project) error {
	f := projectsFile{Projects: projects}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}

	dir := filepath.Dir(ProjectsPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(ProjectsPath(), data, 0o644)
}

// RegisterProject adds a project to the registry.
func RegisterProject(name, path string) error {
	projects, err := LoadProjects()
	if err != nil {
		return err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Check for duplicate name or path
	for _, p := range projects {
		if p.Name == name {
			return fmt.Errorf("project %q is already registered (path: %s)", name, p.Path)
		}
		if p.Path == absPath {
			return fmt.Errorf("path %s is already registered as project %q", absPath, p.Name)
		}
	}

	projects = append(projects, Project{Name: name, Path: absPath})
	return SaveProjects(projects)
}

// UnregisterProject removes a project by name or path.
func UnregisterProject(nameOrPath string) error {
	projects, err := LoadProjects()
	if err != nil {
		return err
	}

	absPath, _ := filepath.Abs(nameOrPath)

	var remaining []Project
	found := false
	for _, p := range projects {
		if p.Name == nameOrPath || p.Path == nameOrPath || p.Path == absPath {
			found = true
			continue
		}
		remaining = append(remaining, p)
	}

	if !found {
		return fmt.Errorf("project %q not found in registry", nameOrPath)
	}

	return SaveProjects(remaining)
}

// BuildDAGSources builds the list of DAG sources from the global dir + registered projects + cwd.
func BuildDAGSources() []DAGSource {
	var sources []DAGSource

	// 1. Global dags dir
	globalDir := filepath.Join(ConfigDir(), "dags")
	sources = append(sources, DAGSource{Name: "global", Dir: globalDir})

	// 2. Registered projects
	projects, _ := LoadProjects()
	for _, p := range projects {
		dagDir := filepath.Join(p.Path, ".daggle")
		sources = append(sources, DAGSource{Name: p.Name, Dir: dagDir})
	}

	// 3. .daggle/ in cwd (if not already covered)
	if cwd, err := os.Getwd(); err == nil {
		localDir := filepath.Join(cwd, ".daggle")
		if info, err := os.Stat(localDir); err == nil && info.IsDir() {
			if !sourceContains(sources, localDir) {
				sources = append(sources, DAGSource{Name: filepath.Base(cwd), Dir: localDir})
			}
		}
	}

	return sources
}

// CollectDAGNames returns all DAG names across sources, mapped to their source name.
// Used for collision detection.
func CollectDAGNames(sources []DAGSource) map[string]string {
	names := make(map[string]string) // dag name -> source name
	for _, src := range sources {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			if name == "base.yaml" || name == "base.yml" {
				continue
			}
			dagName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
			if _, exists := names[dagName]; !exists {
				names[dagName] = src.Name
			}
		}
	}
	return names
}

func sourceContains(sources []DAGSource, dir string) bool {
	for _, s := range sources {
		if s.Dir == dir {
			return true
		}
	}
	return false
}
