package protocol

type Request struct {
	Operation string `json:"operation"`
}

type Response struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	State   *State `json:"state,omitempty"`
}

type State struct {
	Service   string `json:"service"`
	Recording bool   `json:"recording"`
	Model     bool   `json:"model"`
	Runtime   bool   `json:"runtime"`
	Paste     bool   `json:"paste"`
}
