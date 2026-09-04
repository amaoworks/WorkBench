package contracts

import (
	"io/fs"
	"net/http"
)

type ModuleID string

type ModuleManifest struct {
	ID              ModuleID         `json:"id"`
	Name            string           `json:"name"`
	Version         string           `json:"version"`
	ContractVersion int              `json:"contractVersion"`
	Icon            string           `json:"icon"`
	Navigation      []NavigationItem `json:"navigation"`
}

type NavigationItem struct {
	Label   string `json:"label"`
	Route   string `json:"route"`
	PageKey string `json:"pageKey"`
	Order   int    `json:"order"`
}

type MigrationSet struct {
	FS  fs.FS
	Dir string
}

type Module interface {
	Manifest() ModuleManifest
	Migrations() MigrationSet
	Register(ModuleRegistrar) error
}

type ModuleRegistrar interface {
	Handle(method, pattern string, handler http.Handler) error
	Consume(consumer EventConsumer) error
	Tool(tool AITool) error
	Widget(widget WidgetDefinition) error
	Job(job JobDefinition) error
}
