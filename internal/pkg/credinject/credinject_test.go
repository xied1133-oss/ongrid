package credinject

import "testing"

func TestResolveEnvAndFiles(t *testing.T) {
	fields := map[string]string{"secret_id": "AKID", "secret_key": "SK/+=", "region": "ap-guangzhou"}
	envSpec := map[string]string{
		"TENCENTCLOUD_SECRET_ID":  "{{secret_id}}",
		"TENCENTCLOUD_SECRET_KEY": "{{secret_key}}",
		"TENCENTCLOUD_REGION":     "{{region}}",
		"COMPOSITE":               "id={{secret_id}};missing={{nope}}",
	}
	fileSpec := []FileSpec{{Path: "/tmp/creds", Content: "[default]\nkey={{secret_key}}\n", Mode: "0640"}}

	plan, missing, err := Resolve(envSpec, fileSpec, fields)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Env["TENCENTCLOUD_SECRET_ID"] != "AKID" || plan.Env["TENCENTCLOUD_SECRET_KEY"] != "SK/+=" {
		t.Fatalf("env not injected: %+v", plan.Env)
	}
	if plan.Env["COMPOSITE"] != "id=AKID;missing=" {
		t.Fatalf("composite/missing expansion wrong: %q", plan.Env["COMPOSITE"])
	}
	if len(missing) != 1 || missing[0] != "nope" {
		t.Fatalf("missing = %v, want [nope]", missing)
	}
	if len(plan.Files) != 1 || plan.Files[0].Mode != 0o640 || plan.Files[0].Content != "[default]\nkey=SK/+=\n" {
		t.Fatalf("file plan wrong: %+v", plan.Files)
	}
}

func TestResolveBadMode(t *testing.T) {
	if _, _, err := Resolve(nil, []FileSpec{{Path: "/x", Mode: "notoctal"}}, nil); err == nil {
		t.Fatal("want error on bad mode")
	}
}
