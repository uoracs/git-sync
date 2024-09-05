package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"slices"

	"gopkg.in/yaml.v3"
)

type repositoryConfig struct {
	Name    string   `yaml:"name"`
	Local   string   `yaml:"local"`
	Remote  string   `yaml:"remote"`
	Branch  string   `yaml:"branch"`
	ApiKeys []string `yaml:"api_keys"`
}

func (rc repositoryConfig) validKey(key string) bool {
	return slices.Contains(rc.ApiKeys, key)
}

type serverConfig struct {
	Address      string             `yaml:"address"`
	Port         string             `yaml:"port"`
	MasterKey    string             `yaml:"master_key"`
	Repositories []repositoryConfig `yaml:"repositories"`
	LogDirectory string             `yaml:"log_directory"`
}

func (sc serverConfig) getRepository(name string) (*repositoryConfig, error) {
	for _, r := range sc.Repositories {
		if r.Name == name {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("failed to find configured repository with name: %s", name)
}

func (sc serverConfig) keyExists(key string) bool {
	for _, r := range sc.Repositories {
		if slices.Contains(r.ApiKeys, key) {
			return true
		}
	}
	return false
}

type repositoryRequest struct {
	Name string `json:"name"`
}

func getConfigPath() (string, error) {
	defaultPath := "/etc/gic-sync/config.yaml"
	path, found := os.LookupEnv("GIT_SYNC_CONFIG_PATH")
	if found {
		return path, nil
	}
	_, err := os.Stat(defaultPath)
	if err != nil {
		return "", fmt.Errorf("failed to find config file. use either GIT_SYNC_CONFIG_PATH or store config at /etc/git-sync/config.yaml")
	}
	return defaultPath, nil
}

func loadConfig(path string) (*serverConfig, error) {
	var sc serverConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}
	err = yaml.Unmarshal(data, &sc)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file: %v", err)
	}
	return &sc, nil
}

func processConfig(c *serverConfig) error {
	if c.Address == "" {
		c.Address = ""
	}
	if c.Port == "" {
		c.Port = "8654"
	}
	if c.LogDirectory == "" {

	}
	for i, r := range c.Repositories {
		if r.Name == "" {
			return fmt.Errorf("repository config missing name value")
		}
		if r.Local == "" {
			return fmt.Errorf("repository config missing local value")
		}
		if r.Remote == "" {
			return fmt.Errorf("repository config missing remote value")
		}
		if r.Branch == "" {
			r.Branch = "main"
		}
		if c.MasterKey != "" {
			c.Repositories[i].ApiKeys = append(r.ApiKeys, c.MasterKey)
		}
	}
	return nil
}

func localPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func logOperation(r repositoryConfig) {
	slog.Info(r.Name, "local", r.Local, "remote", r.Remote, "branch", r.Branch)
}

func fetchRepository(r repositoryConfig) error {
	cmd := exec.Command("git", "fetch", "origin", r.Branch)
	cmd.Dir = r.Local
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to fetch origin: %v", err)
	}
	return nil
}

func resetRepository(r repositoryConfig) error {
	remote := fmt.Sprintf("origin/%s", r.Branch)
	cmd := exec.Command("git", "reset", "--hard", remote)
	cmd.Dir = r.Local
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to reset origin: %v", err)
	}
	return nil
}

func cleanRepository(r repositoryConfig) error {
	cmd := exec.Command("git", "clean", "-fdx")
	cmd.Dir = r.Local
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to clean untracked files: %v", err)
	}
	return nil
}

func syncRepository(r repositoryConfig) error {
	err := fetchRepository(r)
	if err != nil {
		return fmt.Errorf("failed to sync repository: %v", err)
	}
	err = resetRepository(r)
	if err != nil {
		return fmt.Errorf("failed to sync repository: %v", err)
	}
	err = cleanRepository(r)
	if err != nil {
		return fmt.Errorf("failed to clean repository: %v", err)
	}
	return nil
}

func main() {
	configPath, err := getConfigPath()
	if err != nil {
		fmt.Printf("failed to get config path: %v\n", err)
		os.Exit(1)
	}

	config, err := loadConfig(configPath)
	if err != nil {
		fmt.Printf("failed to load config: %v\n", err)
		os.Exit(1)
	}

	err = processConfig(config)
	if err != nil {
		fmt.Printf("error in config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%#v\n", config)

	connStr := fmt.Sprintf("%s:%s", config.Address, config.Port)

	router := http.NewServeMux()
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check the key first
		reqKey := r.Header.Get("X-GIT-SYNC-KEY")
		if reqKey == "" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized\n")
			return
		}
		if !config.keyExists(reqKey) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized\n")
			return
		}
		// key is valid in SOME repo, start processing
		// return GET requests with nothing
		if r.Method == "GET" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "OK\n")
			return
		}
		// fail early to reduce indentation
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprintf(w, "GET or POST only\n")
			return
		}
		// validate payload
		var repReq repositoryRequest
		err := json.NewDecoder(r.Body).Decode(&repReq)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad Request: Failed to unmarshal json body: %v\n", err)
			return
		}
		if repReq.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad Request: Repository name not provided\n")
			return
		}
		repoConfig, err := config.getRepository(repReq.Name)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "Bad Request: Repository not found: %v\n", err)
			return
		}
		// validate key exists in repo config
		if !repoConfig.validKey(reqKey) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized\n")
			return
		}
		// all good
		logOperation(*repoConfig)
		syncRepository(*repoConfig)
	})

	fmt.Printf("Listening on %s\n", connStr)
	err = http.ListenAndServe(connStr, router)
	if err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}
}
