package torrent

// Every string the engine hands the interface lives here. Labels are in
// title case; messages are sentences.

const (
	strStopped     = "Stopped"
	strChecking    = "Checking"
	strMetadata    = "Fetching Metadata"
	strDownloading = "Downloading"
	strSeeding     = "Seeding"
	strFailed      = "Error"
	strFinished    = "Download finished"

	strAnnouncing   = "Announcing"
	strTrackerOK    = "Working"
	strTrackerError = "Not Working"

	strNoSuchTorrent = "no such torrent"
	strAlreadyAdded  = "%s is already added"
	strNoInfoYet     = "the torrent's metadata has not arrived yet"
)

func (s State) String() string {
	return [...]string{strStopped, strChecking, strMetadata, strDownloading, strSeeding, strFailed}[s]
}
