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
	cf.DatabaseUrl = os.Getenv("CodeAtlas_DB_URL");
	if cf.DatabaseUrl == "" {
		return nil,fmt.Errorf("CodeAtlas_DB_URL is not set")
	}
	cf.Jwtsecret = os.Getenv("CodeAtlas_JWT_secret");
	if cf.Jwtsecret == "" {
		return nil,fmt.Errorf("CodeAtlas_JWT_secret is not set")
	}
	cf.encryptionkey = os.Getenv("CodeAtlas_encryption_key");
	if cf.encryptionkey == "" {
		return nil,fmt.Errorf("CodeAtlas_encryption_key is not set")
	}
	cf.GithubClientId = os.Getenv("CodeAtlas_Github_client_id");
	if cf.GithubClientId == "" {
		return nil,fmt.Errorf("CodeAtlas_Github_client_id is not set")
	}
	cf.GithubClientSecret = os.Getenv("CodeAtlas_Github_client_secret");
	if cf.GithubClientSecret == "" {
		return nil,fmt.Errorf("CodeAtlas_Github_client_secret is not set")
	}
	cf.GitlabClientId = os.Getenv("CodeAtlas_Gitlab_client_id");
	if cf.GitlabClientId == "" {
		return nil,fmt.Errorf("CodeAtlas_Gitlab_client_id is not set")
	}
	cf.GitlabClientSecret = os.Getenv("CodeAtlas_Gitlab_client_secret");
	if cf.GitlabClientSecret == "" {
		return nil,fmt.Errorf("CodeAtlas_Gitlab_client_secret is not set")
	}
	cf.AppbaseUrl = os.Getenv("CodeAtlas_App_base_url");
	if cf.AppbaseUrl == "" {
		return nil,fmt.Errorf("CodeAtlas_App_base_url is not set")
	}
	portStr := os.Getenv("CodeAtlas_port");
	if portStr == "" {
		return nil,fmt.Errorf("CodeAtlas_port is not set")
	}
	var err error
	cf.Port,err = strconv.Atoi(portStr)
	if err != nil {
		return nil,fmt.Errorf("CodeAtlas_port is not a valid integer")
	}

	return cf,nil
}