package billship

import "testing"

func TestValidateRecord(t *testing.T) {
	base := Record{SiteID: "s", Model: "gpt-4", SourceType: "new-api", Body: []byte("{}")}
	tests := []struct {
		name string
		mut  func(r *Record)
		want error
	}{
		{"ok", func(*Record) {}, nil},
		{"empty site", func(r *Record) { r.SiteID = "" }, errEmptyAttr},
		{"empty model", func(r *Record) { r.Model = "" }, errEmptyAttr},
		{"empty source", func(r *Record) { r.SourceType = "" }, errEmptyAttr},
		{"empty body", func(r *Record) { r.Body = nil }, errEmptyBody},
		{"body over max", func(r *Record) { r.Body = make([]byte, maxMessageBytes+1) }, errTooLarge},
		{"body at max plus attrs over", func(r *Record) { r.Body = make([]byte, maxMessageBytes) }, errTooLarge},
		{"near max within budget ok", func(r *Record) { r.Body = make([]byte, maxMessageBytes-100) }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			tt.mut(&r)
			if got := validate(r); got != tt.want {
				t.Errorf("validate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordSize(t *testing.T) {
	r := Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("hello")}
	// body(5)
	//  + site_id: key(7)+type(6)+val "s"(1)          = 14
	//  + model:   key(5)+type(6)+val "m"(1)          = 12
	//  + source:  key(11)+type(6)+val "new-api"(7)   = 24
	//  = 5 + 14 + 12 + 24 = 55
	if got := recordSize(r); got != 55 {
		t.Errorf("recordSize() = %d, want 55", got)
	}
}
