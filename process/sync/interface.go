package sync

// forkDetector is the interface needed by base fork detector to deal with shards and meta nodes
type forkDetector interface {
	computeFinalCheckpoint()
}
