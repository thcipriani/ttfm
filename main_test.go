package main

import "testing"

func TestRefFromGitLog(t *testing.T) {
	var actual, expected string

	actual = refFromGitLog("refs/changes/23/123/6")
	expected = "refs/changes/23/123/meta"
	if (actual != expected) {
		t.Errorf("%s == %s", actual, expected)
	}

	actual = refFromGitLog("origin/master, refs/changes/23/123/6")
	expected = "refs/changes/23/123/meta"
	if (actual != expected) {
		t.Errorf("%s == %s", actual, expected)
	}


	actual = refFromGitLog("REL1_31, refs/changes/23/123/6")
	expected = "refs/changes/23/123/meta"
	if (actual != expected) {
		t.Errorf("%s == %s", actual, expected)
	}
}
