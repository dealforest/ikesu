package guard

// CopyGuardBinaryForTest lets the external guard_test package use the one
// shared build of cmd/ikesu instead of building it again.
var CopyGuardBinaryForTest = copyGuardBinary
