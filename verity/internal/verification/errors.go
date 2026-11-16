package verification

import "errors"

var (
	ErrMissingIdentity     = errors.New("verification run is missing an identity")
	ErrUnsupportedSchema   = errors.New("unsupported verification schema version")
	ErrInvalidCandidate    = errors.New("candidate must be a full Git SHA or SHA-256 digest")
	ErrInvalidCheck        = errors.New("check result is incomplete or has an unknown outcome")
	ErrUnsupportedManifest = errors.New("unsupported verification manifest version")
	ErrInvalidManifest     = errors.New("verification manifest is invalid")
	ErrEmptyManifest       = errors.New("verification manifest has no checks")
	ErrUnsafeCommand       = errors.New("command must be an executable and argv, without shell syntax")
	ErrInvalidProfile      = errors.New("unknown verification profile")
	ErrNotApproved         = errors.New("verification plan has not been explicitly approved")
	ErrCandidateChanged    = errors.New("candidate revision changed; verification result is stale")
	ErrUnsafeArchive       = errors.New("candidate archive contains an unsafe path or file type")
	ErrArchiveTooLarge     = errors.New("candidate source archive exceeds the configured size limit")
)
