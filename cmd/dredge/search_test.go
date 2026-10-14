package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/match"
)

func withHits(r *rig) {
	r.search.results["barracuda"] = []match.Candidate{
		cand("USSM10009075", "Barracuda", "Heart", 261933),
		cand("USSM10009075", "Barracuda", "Heart", 261933), // the same recording on another release
		cand("USXX10000001", "Barracuda (Live)", "Heart", 300000),
	}
}

func TestSearchListsNumberedRecordingsAndDownloadsNothing(t *testing.T) {
	r := newRig(t)
	withHits(r)
	if err := r.run("search", "barracuda"); err != nil {
		t.Fatal(err)
	}
	out := r.out.String()
	for _, want := range []string{"1  Heart - Barracuda", "4:21", "USSM10009075", "2  Heart - Barracuda (Live)", "--get N"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "USSM10009075") != 1 {
		t.Errorf("the same recording is listed twice:\n%s", out)
	}
	if r.exists("Singles/Heart/Barracuda.flac") {
		t.Error("search downloaded something")
	}
}

func TestSearchJoinsSeveralWordsIntoOneQuery(t *testing.T) {
	r := newRig(t)
	r.search.results["heart barracuda"] = []match.Candidate{cand("USSM10009075", "Barracuda", "Heart", 261933)}
	if err := r.run("search", "heart", "barracuda"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "Heart - Barracuda") {
		t.Errorf("output:\n%s", r.out.String())
	}
}

func TestSearchWithNoHitsSaysSo(t *testing.T) {
	r := newRig(t)
	if err := r.run("search", "zzz"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "no downloadable matches") {
		t.Errorf("output:\n%s", r.out.String())
	}
}

func TestSearchGetDownloadsThePickedResult(t *testing.T) {
	r := newRig(t)
	withHits(r)
	if err := r.run("search", "barracuda", "--get", "1"); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if !r.exists("Singles/Heart/Barracuda.flac") {
		t.Errorf("not downloaded:\n%s", r.out.String())
	}
	if err := r.run("search", "barracuda", "--get", "1"); err != nil || !strings.Contains(r.out.String(), "have") {
		t.Errorf("second run should find it in the library: %v\n%s", err, r.out.String())
	}
}

func TestSearchGetRejectsANumberOutsideTheList(t *testing.T) {
	r := newRig(t)
	withHits(r)
	err := r.run("search", "barracuda", "--get", "9")
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("err = %v, want a usage error", err)
	}
}

func TestSearchNeedsAQuery(t *testing.T) {
	r := newRig(t)
	var ue usageError
	if err := r.run("search"); !errors.As(err, &ue) {
		t.Errorf("err = %v, want a usage error", err)
	}
}
