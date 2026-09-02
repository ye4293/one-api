package billship

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestBuildEntry(t *testing.T) {
	r := Record{SiteID: "site-1", Model: "gpt-4", SourceType: "new-api", Body: []byte(`{"id":1}`)}
	e := buildEntry("0", r)

	if aws.ToString(e.Id) != "0" {
		t.Errorf("Id = %q, want 0", aws.ToString(e.Id))
	}
	if aws.ToString(e.MessageBody) != `{"id":1}` {
		t.Errorf("MessageBody = %q", aws.ToString(e.MessageBody))
	}
	for key, want := range map[string]string{
		attrSiteID: "site-1", attrModel: "gpt-4", attrSourceType: "new-api",
	} {
		av, ok := e.MessageAttributes[key]
		if !ok {
			t.Fatalf("missing attribute %q", key)
		}
		if aws.ToString(av.DataType) != "String" {
			t.Errorf("%s DataType = %q, want String", key, aws.ToString(av.DataType))
		}
		if aws.ToString(av.StringValue) != want {
			t.Errorf("%s = %q, want %q", key, aws.ToString(av.StringValue), want)
		}
	}
}
