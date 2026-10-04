package notification

import "testing"

func TestBatchResult_Finalize(t *testing.T) {
	tests := []struct {
		name   string
		result BatchResult
		want   bool
	}{
		{"clean run", BatchResult{KeywordsUpdated: 200}, true},
		{"failed keywords", BatchResult{KeywordsUpdated: 78, KeywordsFailed: 122}, false},
		{"failed tracked keywords", BatchResult{TrackedKeywordsFailed: 1}, false},
		{"errors", BatchResult{Errors: []string{"boom"}}, false},
	}
	for _, tt := range tests {
		r := tt.result
		r.Success = true
		r.Finalize()
		if r.Success != tt.want {
			t.Errorf("%s: Success = %v, want %v", tt.name, r.Success, tt.want)
		}
	}
}
