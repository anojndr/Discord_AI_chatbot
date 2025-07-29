package llm

// AudioFile represents an audio file to be processed by the LLM.
type AudioFile struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
	URL      string `json:"url,omitempty"`
}
