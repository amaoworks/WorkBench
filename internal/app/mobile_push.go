package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"workbench/internal/capabilities/mobilepush"
)

type MobilePushDeployment struct {
	Enabled               bool
	APNsP8File            string
	APNsTeamID            string
	APNsKeyID             string
	APNsBundleID          string
	APNsSandbox           bool
	FCMServiceAccountFile string
}

func loadMobilePushConfig(deployment MobilePushDeployment) (mobilepush.Config, error) {
	config := mobilepush.Config{Enabled: deployment.Enabled}
	if !deployment.Enabled {
		return config, nil
	}

	apnsConfigured := deployment.APNsP8File != "" || deployment.APNsTeamID != "" || deployment.APNsKeyID != "" || deployment.APNsBundleID != "" || deployment.APNsSandbox
	if apnsConfigured {
		if deployment.APNsP8File == "" || deployment.APNsTeamID == "" || deployment.APNsKeyID == "" || deployment.APNsBundleID == "" {
			return mobilepush.Config{}, errors.New("APNs mobile push requires p8 file, team ID, key ID, and bundle ID")
		}
		privateKey, err := os.ReadFile(deployment.APNsP8File)
		if err != nil {
			return mobilepush.Config{}, fmt.Errorf("read APNs p8 file: %w", err)
		}
		config.APNs = &mobilepush.APNsConfig{
			TeamID: deployment.APNsTeamID, KeyID: deployment.APNsKeyID, BundleID: deployment.APNsBundleID,
			PrivateKey: privateKey, Sandbox: deployment.APNsSandbox,
		}
	}

	if deployment.FCMServiceAccountFile != "" {
		raw, err := os.ReadFile(deployment.FCMServiceAccountFile)
		if err != nil {
			return mobilepush.Config{}, fmt.Errorf("read FCM service account file: %w", err)
		}
		var account struct {
			Type        string `json:"type"`
			ProjectID   string `json:"project_id"`
			ClientEmail string `json:"client_email"`
			PrivateKey  string `json:"private_key"`
		}
		if json.Unmarshal(raw, &account) != nil || account.Type != "service_account" || account.ProjectID == "" || account.ClientEmail == "" || account.PrivateKey == "" {
			return mobilepush.Config{}, errors.New("FCM service account file is invalid")
		}
		config.FCM = &mobilepush.FCMConfig{ProjectID: account.ProjectID, ClientEmail: account.ClientEmail, PrivateKey: []byte(account.PrivateKey)}
	}
	if config.APNs == nil && config.FCM == nil {
		return mobilepush.Config{}, errors.New("mobile push requires at least one APNs or FCM provider")
	}
	return config, nil
}
