package tokentracer

import "time"

const globalQueryVersion = 2

type LedgerQuery struct {
	View    string `json:"view"`
	Period  string `json:"period"`
	Project string `json:"project,omitempty"`
	Session string `json:"session,omitempty"`
	Model   string `json:"model,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Search  string `json:"search,omitempty"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
}

type RequestSummary struct {
	Requests      int `json:"requests"`
	Attempts      int `json:"attempts"`
	Unknown       int `json:"unknown"`
	Reported      int `json:"reported"`
	Total         int `json:"total"`
	Input         int `json:"input"`
	InputKnown    int `json:"inputKnown"`
	Output        int `json:"output"`
	OutputKnown   int `json:"outputKnown"`
	CacheRead     int `json:"cacheRead"`
	CacheCreation int `json:"cacheCreation"`
	CacheKnown    int `json:"cacheKnown"`
	Reasoning     int `json:"reasoning"`
	Running       int `json:"running"`
}

type LedgerRequestRow struct {
	ID         string         `json:"id"`
	InstanceID string         `json:"instance_id"`
	ProjectID  string         `json:"project_id"`
	Model      string         `json:"model"`
	StartedAt  time.Time      `json:"started_at"`
	Status     string         `json:"status"`
	Purpose    string         `json:"purpose"`
	Summary    RequestSummary `json:"summary"`
}

type LedgerGroup struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	ProjectID string         `json:"project_id,omitempty"`
	Summary   RequestSummary `json:"summary"`
}

type LedgerTool struct {
	Name        string `json:"name"`
	Occurrences int    `json:"occurrences"`
	Definitions int    `json:"definitions"`
	Arguments   int    `json:"arguments"`
	Results     int    `json:"results"`
	Unknown     int    `json:"unknown"`
}

type LedgerTrendBin struct {
	Time  int64 `json:"time"`
	Total int   `json:"total"`
	Count int   `json:"count"`
}

type LedgerPagination struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Total  int `json:"total"`
}

type LedgerPage struct {
	Version         int                `json:"version"`
	GeneratedAt     time.Time          `json:"generated_at"`
	Instances       []LedgerInstance   `json:"instances"`
	Issues          []LedgerIssue      `json:"issues"`
	Query           LedgerQuery        `json:"query"`
	Summary         RequestSummary     `json:"summary"`
	StoredRequests  int                `json:"stored_requests"`
	ProjectCount    int                `json:"project_count"`
	ActiveInstances int                `json:"active_instances"`
	Requests        []LedgerRequestRow `json:"requests"`
	Groups          []LedgerGroup      `json:"groups"`
	Tools           []LedgerTool       `json:"tools"`
	Trend           []LedgerTrendBin   `json:"trend"`
	Pagination      LedgerPagination   `json:"pagination"`
}

func (s RequestSummary) add(v RequestSummary) RequestSummary {
	return RequestSummary{Requests: s.Requests + v.Requests, Attempts: s.Attempts + v.Attempts, Unknown: s.Unknown + v.Unknown, Reported: s.Reported + v.Reported, Total: s.Total + v.Total, Input: s.Input + v.Input, InputKnown: s.InputKnown + v.InputKnown, Output: s.Output + v.Output, OutputKnown: s.OutputKnown + v.OutputKnown, CacheRead: s.CacheRead + v.CacheRead, CacheCreation: s.CacheCreation + v.CacheCreation, CacheKnown: s.CacheKnown + v.CacheKnown, Reasoning: s.Reasoning + v.Reasoning, Running: s.Running + v.Running}
}
