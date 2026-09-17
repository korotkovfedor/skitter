package crawler

import "fmt"

type Stage string

const (
	StageFetch        Stage = "fetch"
	StageExtractLinks Stage = "extract links"
)

type CrawlError struct {
	Stage Stage
	Err   error
}

func (e *CrawlError) Error() string {
	return fmt.Sprintf("%s: %v", e.Stage, e.Err)
}

func (e *CrawlError) Unwrap() error {
	return e.Err
}
