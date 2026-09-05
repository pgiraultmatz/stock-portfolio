package twitter

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubFetcher struct {
	tweets []Tweet
	err    error
	calls  int
}

func (f *stubFetcher) GetRecentTweets(_ context.Context, _ string, _ int) ([]Tweet, error) {
	f.calls++
	return f.tweets, f.err
}

func TestResolveNitterInstancesUsesConfiguredValues(t *testing.T) {
	t.Setenv("NITTER_INSTANCES", "https://env.example")

	instances := resolveNitterInstances([]string{"https://config.example"})
	if len(instances) < 3 || instances[0] != "https://config.example" || instances[1] != "https://env.example" {
		t.Fatalf("expected configured and env instances before defaults, got %#v", instances)
	}
}

func TestResolveNitterInstancesUsesEnvFallback(t *testing.T) {
	t.Setenv("NITTER_INSTANCES", " https://one.example,https://two.example ,, ")

	instances := resolveNitterInstances(nil)
	if len(instances) < 4 || instances[0] != "https://one.example" || instances[1] != "https://two.example" {
		t.Fatalf("expected env instances before defaults, got %#v", instances)
	}
}

func TestResolveNitterInstancesUsesDefaults(t *testing.T) {
	t.Setenv("NITTER_INSTANCES", "")

	instances := resolveNitterInstances(nil)
	if len(instances) < 2 {
		t.Fatalf("expected multiple default Nitter instances, got %#v", instances)
	}
}

func TestResolveNitterInstancesDeduplicates(t *testing.T) {
	t.Setenv("NITTER_INSTANCES", "https://nitter.tiekoetter.com/")

	instances := resolveNitterInstances([]string{"https://nitter.tiekoetter.com"})
	count := 0
	for _, instance := range instances {
		if instance == "https://nitter.tiekoetter.com" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected tiekoetter once, got %#v", instances)
	}
}

func TestFallbackFetcherUsesPrimaryWhenItWorks(t *testing.T) {
	primary := &stubFetcher{tweets: []Tweet{{ID: "primary", CreatedAt: time.Now()}}}
	fallback := &stubFetcher{tweets: []Tweet{{ID: "fallback", CreatedAt: time.Now()}}}

	tweets, err := (FallbackFetcher{Primary: primary, Fallback: fallback}).GetRecentTweets(context.Background(), "user", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tweets) != 1 || tweets[0].ID != "primary" {
		t.Fatalf("expected primary tweets, got %#v", tweets)
	}
	if primary.calls != 1 || fallback.calls != 0 {
		t.Fatalf("expected only primary call, got primary=%d fallback=%d", primary.calls, fallback.calls)
	}
}

func TestFallbackFetcherUsesFallbackWhenPrimaryFails(t *testing.T) {
	primary := &stubFetcher{err: errors.New("primary down")}
	fallback := &stubFetcher{tweets: []Tweet{{ID: "fallback", CreatedAt: time.Now()}}}

	tweets, err := (FallbackFetcher{Primary: primary, Fallback: fallback}).GetRecentTweets(context.Background(), "user", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tweets) != 1 || tweets[0].ID != "fallback" {
		t.Fatalf("expected fallback tweets, got %#v", tweets)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("expected primary and fallback calls, got primary=%d fallback=%d", primary.calls, fallback.calls)
	}
}
