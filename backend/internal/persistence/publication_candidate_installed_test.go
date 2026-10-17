package persistence

import "testing"

func TestPublicationCandidateInstalledObservationRequiresCompleteIdentity(t *testing.T) {
	size, mtime := int64(10), int64(20)
	valid := PublicationCandidateInstalledObservation{
		Version: 1, TargetRelativePath: "root/track.flac",
		Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
	}
	if !validPublicationCandidateInstalledObservation(valid) {
		t.Fatal("valid installed observation was rejected")
	}
	cases := map[string]func(*PublicationCandidateInstalledObservation){
		"version": func(o *PublicationCandidateInstalledObservation) { o.Version++ },
		"path":    func(o *PublicationCandidateInstalledObservation) { o.TargetRelativePath = "../track.flac" },
		"size":    func(o *PublicationCandidateInstalledObservation) { o.Identity.SizeBytes = nil },
		"mtime":   func(o *PublicationCandidateInstalledObservation) { o.Identity.MTime = nil },
		"digest":  func(o *PublicationCandidateInstalledObservation) { o.Identity.SHA256 = "ABC" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := valid
			invalid.Identity = clonePublicationFileIdentity(valid.Identity)
			mutate(&invalid)
			if validPublicationCandidateInstalledObservation(invalid) {
				t.Fatal("invalid installed observation was accepted")
			}
		})
	}
}
