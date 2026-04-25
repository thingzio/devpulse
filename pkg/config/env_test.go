package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImportOrg(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		assert.Empty(t, ImportOrg())
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("IMPORT_ORG", "myorg")
		assert.Equal(t, "myorg", ImportOrg())
	})
}

func TestImportRepo(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		assert.Empty(t, ImportRepo())
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("IMPORT_REPO", "myrepo")
		assert.Equal(t, "myrepo", ImportRepo())
	})
}

func TestServerConfig(t *testing.T) {
	t.Run("shutdown timeout default", func(t *testing.T) {
		assert.Equal(t, 5, ServerShutdownTimeout())
	})
	t.Run("trigger timeout default", func(t *testing.T) {
		assert.Equal(t, 30, ServerTriggerTimeout())
	})
	t.Run("oauth rate limit default", func(t *testing.T) {
		assert.Equal(t, 20, OAuthRateLimit())
	})
	t.Run("repo search rate limit default", func(t *testing.T) {
		assert.Equal(t, 30, RepoSearchRateLimit())
	})
	t.Run("query param min days default", func(t *testing.T) {
		assert.Equal(t, 14, QueryParamMinDays())
	})
	t.Run("query param max days default", func(t *testing.T) {
		assert.Equal(t, 3650, QueryParamMaxDays())
	})
}

func TestImportJobName(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		assert.Empty(t, ImportJobName())
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("IMPORT_JOB_NAME", "projects/p/locations/l/jobs/j")
		assert.Equal(t, "projects/p/locations/l/jobs/j", ImportJobName())
	})
}

func TestGetEnv(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert.Equal(t, "fallback", GetEnv("DEVPULSE_TEST_UNSET_VAR", "fallback"))
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_VAR", "hello")
		assert.Equal(t, "hello", GetEnv("DEVPULSE_TEST_VAR", "fallback"))
	})
}

func TestGetEnvAsInt(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert.Equal(t, 42, GetEnvAsInt("DEVPULSE_TEST_UNSET_INT", 42))
	})
	t.Run("valid", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_INT", "10")
		assert.Equal(t, 10, GetEnvAsInt("DEVPULSE_TEST_INT", 42))
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_INT", "abc")
		assert.Equal(t, 42, GetEnvAsInt("DEVPULSE_TEST_INT", 42))
	})
	t.Run("zero returns default", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_INT", "0")
		assert.Equal(t, 42, GetEnvAsInt("DEVPULSE_TEST_INT", 42))
	})
}

func TestGetEnvAsIntNonNeg(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert.Equal(t, 5, GetEnvAsIntNonNeg("DEVPULSE_TEST_UNSET_NN", 5))
	})
	t.Run("zero allowed", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_NN", "0")
		assert.Equal(t, 0, GetEnvAsIntNonNeg("DEVPULSE_TEST_NN", 5))
	})
	t.Run("negative returns default", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_NN", "-1")
		assert.Equal(t, 5, GetEnvAsIntNonNeg("DEVPULSE_TEST_NN", 5))
	})
}

func TestGetEnvBool(t *testing.T) {
	t.Run("unset is false", func(t *testing.T) {
		assert.False(t, GetEnvBool("DEVPULSE_TEST_UNSET_BOOL"))
	})
	t.Run("true", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_BOOL", "true")
		assert.True(t, GetEnvBool("DEVPULSE_TEST_BOOL"))
	})
	t.Run("1", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_BOOL", "1")
		assert.True(t, GetEnvBool("DEVPULSE_TEST_BOOL"))
	})
	t.Run("false", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_BOOL", "no")
		assert.False(t, GetEnvBool("DEVPULSE_TEST_BOOL"))
	})
}

func TestGetEnvAsFloat(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert.InDelta(t, 3.14, GetEnvAsFloat("DEVPULSE_TEST_UNSET_FLOAT", 3.14), 0.001)
	})
	t.Run("valid", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_FLOAT", "2.5")
		assert.InDelta(t, 2.5, GetEnvAsFloat("DEVPULSE_TEST_FLOAT", 3.14), 0.001)
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("DEVPULSE_TEST_FLOAT", "abc")
		assert.InDelta(t, 3.14, GetEnvAsFloat("DEVPULSE_TEST_FLOAT", 3.14), 0.001)
	})
}

func TestBackfillMaxDays(t *testing.T) {
	assert.Equal(t, 90, BackfillMaxDays())
}

func TestImportDBBatchSize(t *testing.T) {
	assert.Equal(t, 100, ImportDBBatchSize())
}

func TestImportFreshDays(t *testing.T) {
	assert.Equal(t, 21, ImportFreshDays())
}

func TestImportBackfillChunkDays(t *testing.T) {
	assert.Equal(t, 7, ImportBackfillChunkDays())
}

func TestImportWorkers(t *testing.T) {
	assert.Equal(t, 2, ImportWorkers())
}

func TestImportTaskTimeout(t *testing.T) {
	assert.Equal(t, 55, ImportTaskTimeout())
}

func TestCloudRunTaskIndex(t *testing.T) {
	assert.Equal(t, 0, CloudRunTaskIndex())
}

func TestCloudRunTaskCount(t *testing.T) {
	assert.Equal(t, 1, CloudRunTaskCount())
}

func TestCloudRunExecution(t *testing.T) {
	t.Run("default local prefix", func(t *testing.T) {
		assert.Contains(t, CloudRunExecution(), "local-")
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("CLOUD_RUN_EXECUTION", "exec-123")
		assert.Equal(t, "exec-123", CloudRunExecution())
	})
}

func TestSampleRepos(t *testing.T) {
	t.Run("empty when unset", func(t *testing.T) {
		assert.Nil(t, SampleRepos())
	})
	t.Run("single repo", func(t *testing.T) {
		t.Setenv("SAMPLE_REPOS", "etcd-io/etcd")
		got := SampleRepos()
		assert.Len(t, got, 1)
		assert.Equal(t, SampleRepo{Org: "etcd-io", Repo: "etcd"}, got[0])
	})
	t.Run("multiple repos", func(t *testing.T) {
		t.Setenv("SAMPLE_REPOS", "etcd-io/etcd,prometheus/prometheus,containerd/containerd")
		got := SampleRepos()
		assert.Len(t, got, 3)
		assert.Equal(t, "prometheus", got[1].Org)
		assert.Equal(t, "prometheus", got[1].Repo)
	})
	t.Run("trims whitespace", func(t *testing.T) {
		t.Setenv("SAMPLE_REPOS", " etcd-io/etcd , prometheus/prometheus ")
		got := SampleRepos()
		assert.Len(t, got, 2)
		assert.Equal(t, "etcd-io", got[0].Org)
		assert.Equal(t, "etcd", got[0].Repo)
	})
	t.Run("skips malformed entries", func(t *testing.T) {
		t.Setenv("SAMPLE_REPOS", "good/repo,,bad,/nope,also-bad/,ok/fine")
		got := SampleRepos()
		assert.Len(t, got, 2)
		assert.Equal(t, SampleRepo{Org: "good", Repo: "repo"}, got[0])
		assert.Equal(t, SampleRepo{Org: "ok", Repo: "fine"}, got[1])
	})
}

func TestIsSampleRepo(t *testing.T) {
	t.Run("false when unset", func(t *testing.T) {
		assert.False(t, IsSampleRepo("etcd-io", "etcd"))
	})
	t.Run("true for match", func(t *testing.T) {
		t.Setenv("SAMPLE_REPOS", "etcd-io/etcd,prometheus/prometheus")
		assert.True(t, IsSampleRepo("etcd-io", "etcd"))
		assert.True(t, IsSampleRepo("prometheus", "prometheus"))
	})
	t.Run("false for non-match", func(t *testing.T) {
		t.Setenv("SAMPLE_REPOS", "etcd-io/etcd")
		assert.False(t, IsSampleRepo("other", "repo"))
	})
}

func TestGCPProjectID(t *testing.T) {
	assert.Equal(t, "thingzio", GCPProjectID())
}
