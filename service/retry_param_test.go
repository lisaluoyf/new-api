package service

import "testing"

func TestRetryParamSnapshotRestore(t *testing.T) {
	retry := 2
	param := &RetryParam{Retry: &retry}
	param.ResetRetryNextTry()

	snapshot := param.Snapshot()

	param.IncreaseRetry()
	param.IncreaseRetry()
	if param.GetRetry() != 3 {
		t.Fatalf("retry after mutation = %d, want 3", param.GetRetry())
	}

	param.Restore(snapshot)

	param.IncreaseRetry()
	if param.GetRetry() != 2 {
		t.Fatalf("retry after restored reset-next-try = %d, want 2", param.GetRetry())
	}
	param.IncreaseRetry()
	if param.GetRetry() != 3 {
		t.Fatalf("retry after restored reset consumed = %d, want 3", param.GetRetry())
	}
}

func TestRetryParamSnapshotRestoreNilRetry(t *testing.T) {
	param := &RetryParam{}
	snapshot := param.Snapshot()

	param.SetRetry(5)
	param.ResetRetryNextTry()
	param.Restore(snapshot)

	if param.Retry != nil {
		t.Fatalf("retry pointer after restore = %v, want nil", *param.Retry)
	}
	param.IncreaseRetry()
	if param.GetRetry() != 1 {
		t.Fatalf("retry after increase from restored nil = %d, want 1", param.GetRetry())
	}
}
