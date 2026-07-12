package config

import (
	"fmt"
	"os"
	"strconv"
)

type config struct {
	DatabaseUrl string
	Jwtsecret   string
	encryptionkey string
	GithubClientId string
	GithubClientSecret string
	GitlabClientId string
	GitlabClientSecret string
	AppbaseUrl         string
	Port               int
}

func Load() (*config, error) {
	cf := &config{}
	cf.DatabaseUrl = os.Getenv("CODEATLAS_DB_URL");
	if cf.DatabaseUrl == "" {
		return nil,fmt.Errorf("CODEATLAS_DB_URL is not set")
	}
	cf.Jwtsecret = os.Getenv("CODEATLAS_JWT_SECRET");
	if cf.Jwtsecret == "" {
		return nil,fmt.Errorf("CODEATLAS_JWT_SECRET is not set")
	}
	cf.encryptionkey = os.Getenv("CODEATLAS_ENCRYPTION_KEY");
	if cf.encryptionkey == "" {
		return nil,fmt.Errorf("CODEATLAS_ENCRYPTION_KEY is not set")
	}
	cf.GithubClientId = os.Getenv("CODEATLAS_GITHUB_CLIENT_ID");
	if cf.GithubClientId == "" {
		return nil,fmt.Errorf("CODEATLAS_GITHUB_CLIENT_ID is not set")
	}
	cf.GithubClientSecret = os.Getenv("CODEATLAS_GITHUB_CLIENT_SECRET");
	if cf.GithubClientSecret == "" {
		return nil,fmt.Errorf("CODEATLAS_GITHUB_CLIENT_SECRET is not set")
	}
	cf.GitlabClientId = os.Getenv("CODEATLAS_GITLAB_CLIENT_ID");
	if cf.GitlabClientId == "" {
		return nil,fmt.Errorf("CODEATLAS_GITLAB_CLIENT_ID is not set")
	}
	cf.GitlabClientSecret = os.Getenv("CODEATLAS_GITLAB_CLIENT_SECRET");
	if cf.GitlabClientSecret == "" {
		return nil,fmt.Errorf("CODEATLAS_GITLAB_CLIENT_SECRET is not set")
	}
	cf.AppbaseUrl = os.Getenv("CODEATLAS_APP_BASE_URL");
	if cf.AppbaseUrl == "" {
		return nil,fmt.Errorf("CODEATLAS_APP_BASE_URL is not set")
	}
	portStr := os.Getenv("CODEATLAS_PORT");
	if portStr == "" {
		return nil,fmt.Errorf("CODEATLAS_PORT is not set")
	}
	var err error
	cf.Port,err = strconv.Atoi(portStr)
	if err != nil {
		return nil,fmt.Errorf("CODEATLAS_PORT is not a valid integer")
	}

	return cf,nil
}