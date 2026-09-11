package credentialstore

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCopyExistingRefreshMaterialKeepsWhenIncomingOmits(t *testing.T) {
	const oldRefresh = "refresh-must-survive"
	const newAccess = "access-after-import"
	incoming := []byte(`{"access_token":"` + newAccess + `","session_token":"` + newAccess + `"}`)
	existing := []byte(`{"access_token":"access-before","refresh_token":"` + oldRefresh + `"}`)

	got, copied, err := copyExistingRefreshMaterial(incoming, existing)
	if err != nil {
		t.Fatalf("合并失败：%v", err)
	}
	if !copied {
		t.Fatal("入站缺少续期材料时必须拷回已有值；删掉拷回后本断言变红")
	}
	var fields map[string]string
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["access_token"] != newAccess {
		t.Fatalf("访问令牌=%q，期望更新为 %q", fields["access_token"], newAccess)
	}
	if fields["refresh_token"] != oldRefresh {
		t.Fatalf("续期材料=%q，期望保留 %q", fields["refresh_token"], oldRefresh)
	}
}

func TestCopyExistingRefreshMaterialTreatsEmptyAsOmitted(t *testing.T) {
	const oldRefresh = "refresh-must-survive-empty"
	incoming := []byte(`{"access_token":"access-new","refresh_token":"   "}`)
	existing := []byte(`{"refresh_token":"` + oldRefresh + `"}`)

	got, copied, err := copyExistingRefreshMaterial(incoming, existing)
	if err != nil || !copied {
		t.Fatalf("空白续期字段必须当缺席处理：copied=%v err=%v", copied, err)
	}
	var fields map[string]string
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["refresh_token"] != oldRefresh {
		t.Fatalf("空白入站冲掉了已有续期材料：%q", fields["refresh_token"])
	}
}

func TestCopyExistingRefreshMaterialReplacesWhenIncomingProvides(t *testing.T) {
	const newRefresh = "refresh-from-operator"
	incoming := []byte(`{"access_token":"access-new","refresh_token":"` + newRefresh + `"}`)
	existing := []byte(`{"refresh_token":"refresh-old-must-not-win"}`)

	got, copied, err := copyExistingRefreshMaterial(incoming, existing)
	if err != nil {
		t.Fatalf("合并失败：%v", err)
	}
	if copied {
		t.Fatal("入站已带续期材料时不得回拷旧值")
	}
	if string(got) != string(incoming) {
		t.Fatalf("已提供新续期材料时必须保持入站对象：got=%s", got)
	}
}

func TestCopyExistingRefreshMaterialDoesNotInvent(t *testing.T) {
	incoming := []byte(`{"access_token":"access-only"}`)
	existing := []byte(`{"access_token":"old-access"}`)

	got, copied, err := copyExistingRefreshMaterial(incoming, existing)
	if err != nil || copied {
		t.Fatalf("双方都没有续期材料时不得编造：copied=%v err=%v", copied, err)
	}
	if !payloadOmitsRefreshMaterial(got) {
		t.Fatalf("无旧值可保时不得写入续期字段：%s", got)
	}
}

func TestCopyExistingRefreshMaterialRejectsBrokenExistingJSON(t *testing.T) {
	_, copied, err := copyExistingRefreshMaterial([]byte(`{"access_token":"a"}`), []byte(`not-json`))
	if err == nil || copied {
		t.Fatalf("当前明文损坏时必须失败：copied=%v err=%v", copied, err)
	}
	if !strings.Contains(err.Error(), "json") && !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("错误应停留在解析失败，不能带上明文：%v", err)
	}
}

func TestPayloadOmitsRefreshMaterial(t *testing.T) {
	if !payloadOmitsRefreshMaterial([]byte(`{"access_token":"a"}`)) {
		t.Fatal("缺字段应视为未提供")
	}
	if payloadOmitsRefreshMaterial([]byte(`{"refresh_token":"rt"}`)) {
		t.Fatal("非空续期材料不能当成缺席")
	}
	if payloadOmitsRefreshMaterial([]byte(`{`)) {
		t.Fatal("解析失败不得误判为缺席，否则会在损坏入站上尝试解密合并")
	}
}
