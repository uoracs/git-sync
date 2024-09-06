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
	Name   string   `yaml:"name"`
	Local  string   `yaml:"local"`
	Remote string   `yaml:"remote"`
	Branch string   `yaml:"branch"`
	Tokens []string `yaml:"tokens"`
}

func (rc repositoryConfig) validToken(key string) bool {
	return slices.Contains(rc.Tokens, key)
}

type serverConfig struct {
	Address      string             `yaml:"address"`
	Port         string             `yaml:"port"`
	GlobalTokens []string           `yaml:"global_tokens"`
	Repositories []repositoryConfig `yaml:"repositories"`
}

func (sc serverConfig) getRepository(name string) (*repositoryConfig, error) {
	for _, r := range sc.Repositories {
		if r.Name == name {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("failed to find configured repository with name: %s", name)
}

func (sc serverConfig) tokenExists(key string) bool {
	for _, r := range sc.Repositories {
		if slices.Contains(r.Tokens, key) {
			return true
		}
	}
	return false
}

type repositoryRequest struct {
	Name string `json:"name"`
}

func getConfigPath() (string, error) {
	defaultPath := "/etc/git-sync/config.yaml"
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
		c.Address = "0.0.0.0"
	}
	if c.Port == "" {
		c.Port = "8654"
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
		if len(c.GlobalTokens) > 0 {
			for _, t := range c.GlobalTokens {
				c.Repositories[i].Tokens = append(r.Tokens, t)
			}
		}
	}
	return nil
}

func localPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

	connStr := fmt.Sprintf("%s:%s", config.Address, config.Port)

	router := http.NewServeMux()
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check the token first
		reqToken := r.Header.Get("X-GIT-SYNC-TOKEN")
		if reqToken == "" {
			slog.Warn("token not provided", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized\n")
			return
		}
		if !config.tokenExists(reqToken) {
			slog.Warn("invalid token", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized\n")
			return
		}
		// token is valid in SOME repo, start processing
		// return GET requests with nothing
		if r.Method == "GET" {
			slog.Warn("hit GET", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "OK\n")
			return
		}
		// fail early to reduce indentation
		if r.Method != "POST" {
			slog.Warn("invalid method", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprintf(w, "GET or POST only\n")
			return
		}
		// validate payload
		var repReq repositoryRequest
		err := json.NewDecoder(r.Body).Decode(&repReq)
		if err != nil {
			slog.Warn("failed to unmarshal body", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad Request: Failed to unmarshal json body: %v\n", err)
			return
		}
		if repReq.Name == "" {
			slog.Warn("repository name not provided", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad Request: Repository name not provided\n")
			return
		}
		repoConfig, err := config.getRepository(repReq.Name)
		if err != nil {
			slog.Warn("repository not found", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "Bad Request: Repository not found: %v\n", err)
			return
		}
		// validate key exists in repo config
		if !repoConfig.validToken(reqToken) {
			slog.Warn("invalid token for repository", "source_ip", r.Header.Get("x-forwarded-for"))
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized\n")
			return
		}
		// all good
		slog.Info(repoConfig.Name, "local", repoConfig.Local, "remote", repoConfig.Remote, "branch", repoConfig.Branch)

		syncRepository(*repoConfig)
	})

	fmt.Printf("Listening on %s\n", connStr)
	err = http.ListenAndServe(connStr, router)
	if err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}
}
