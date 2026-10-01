package render

// ActivitySummary is factual presentation data. Fitting and footer placement
// belong to the presenter, not callback adapters or lifecycle registries.
type ActivitySummary struct {
	Primary   string
	Compact   string
	Secondary string
	Counts    string
}
