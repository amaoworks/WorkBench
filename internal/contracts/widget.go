package contracts

type WidgetSize string

const (
	WidgetSmall  WidgetSize = "small"
	WidgetMedium WidgetSize = "medium"
	WidgetLarge  WidgetSize = "large"
)

type WidgetDefinition struct {
	ID            string     `json:"id"`
	Module        ModuleID   `json:"module"`
	SchemaVersion int        `json:"schemaVersion"`
	Title         string     `json:"title"`
	WidgetKind    string     `json:"widgetKind"`
	DataRoute     string     `json:"dataRoute"`
	Size          WidgetSize `json:"size"`
	Order         int        `json:"order"`
}
