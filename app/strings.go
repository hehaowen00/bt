package app

import "fmt"

// Every string the interface shows lives here, so wording is changed in one
// place. Labels, titles and menu entries are in title case; messages are
// sentences.

const appName = "bt"

// Panes and tabs.
const (
	strTorrents   = "Torrents"
	strGeneral    = "General"
	strFiles      = "Files"
	strPeers      = "Peers"
	strTrackers   = "Trackers"
	strHelp       = "Help"
	strNoTorrents = "No Torrents"
	strNoMatches  = "No Matches"
	strNoFiles    = "Metadata Not Yet Received"
	strNoPeers    = "No Peers Connected"
	strNoTrackers = "No Trackers"
	strNoneChosen = "No Torrent Selected"
)

// List columns.
const (
	colName   = "Name"
	colSize   = "Size"
	colDone   = "Done"
	colStatus = "Status"
	colDown   = "Down"
	colUp     = "Up"
	colPeers  = "Peers"
	colETA    = "ETA"
	colAddr   = "Address"
	colClient = "Client"
	colFlags  = "Flags"
	colTier   = "Tier"
	colURL    = "URL"
	colSeeds  = "Seeds"
	colLeech  = "Leechers"
	colNext   = "Next"
	colPath   = "Path"
)

// General tab labels.
const (
	lblName     = "Name"
	lblStatus   = "Status"
	lblPieces   = "Pieces"
	lblSpeed    = "Speed"
	lblTransfer = "Transferred"
	lblPeers    = "Peers"
	lblETA      = "Remaining"
	lblSavedIn  = "Saved In"
	lblHash     = "Info Hash"
	lblAdded    = "Added"
	lblComment  = "Comment"
	lblError    = "Error"
	lblPrivate  = "Private"
)

// Values and phrases.
const (
	strOf          = "%s of %s"
	strPieceInfo   = "%d of %d · %s each"
	strPeerInfo    = "%d connected, %d seeding · %d known"
	strRatio       = "ratio %.2f"
	strYes         = "Yes"
	strUnknown     = "Unknown"
	strDone        = "Done"
	strNow         = "Now"
	strPaused      = "Paused"
	strDHTNodes    = "DHT %d"
	strDHTOff      = "DHT off"
	strPortInfo    = "Port %d"
	strNoPort      = "No incoming port"
	strFilterLabel = "Filter: %s"
)

// Prompts and messages.
const (
	promptCommand  = "Command: "
	promptAdd      = "Add (file or magnet): "
	promptSearch   = "Filter: "
	promptRemove   = "Remove %s? (y/n)"
	promptDelete   = "Remove %s and delete its files? (y/n)"
	promptSaveIn   = "Save in: "
	promptMakeFrom = "New torrent from: "
	promptTrackers = "Trackers: "
	promptPrivate  = "Private? (y/n)"
	promptSaveAs   = "Save as: "
	promptReplace  = "Replace %s? (y/n)"
	msgAdded       = "Added %s"
	msgRemoved     = "Removed %s"
	msgDeleted     = "Removed %s and deleted its files"
	msgPaused      = "Paused %s"
	msgResumed     = "Resumed %s"
	msgRechecking  = "Checking %s"
	msgCopied      = "Copied the magnet link"
	msgCopiedHash  = "Copied the info hash"
	msgCopiedPath  = "Copied the path"
	msgFinished    = "%s: %s"
	msgWanted      = "%d files will download"
	msgSkipped     = "%d files skipped"
	msgTheme       = "Theme %s"
	msgHashing     = "Hashing %s: %d%%"
	msgMade        = "Made %s and seeding it"
	errMaking      = "A torrent is already being made"
	errTracker     = "%s is not a tracker URL"
	errNoTorrent   = "No torrent selected"
	errNotFilesTab = "Files can be chosen in the Files tab"
)

// Command palette entries.
const (
	cmdAdd        = "Add Torrent"
	cmdAddTo      = "Add Torrent to Folder"
	cmdMake       = "New Torrent"
	cmdPause      = "Pause or Resume"
	cmdRecheck    = "Check Files Again"
	cmdRemove     = "Remove Torrent"
	cmdDelete     = "Remove Torrent and Delete Files"
	cmdCopyMagnet = "Copy Magnet Link"
	cmdCopyHash   = "Copy Info Hash"
	cmdCopyPath   = "Copy Path"
	cmdFilter     = "Filter by Name"
	cmdNextTheme  = "Next Theme"
	cmdTheme      = "Theme: %s"
	cmdHelp       = "Help"
	cmdQuit       = "Quit"
)

// Help screen sections.
const (
	helpMoving   = "Moving"
	helpTorrents = "Torrents"
	helpFiles    = "Files"
	helpFinding  = "Finding"
	helpCommands = "Commands"
	helpLeaving  = "Leaving"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

type helpKey struct{ keys, what string }

type helpSection struct {
	title string
	keys  []helpKey
}

var helpSections = []helpSection{
	{
		helpMoving,
		[]helpKey{
			{
				"↓ ↑",
				"Move",
			},
			{
				"PgDn PgUp  Home End",
				"Move a screen, or to the first or last row",
			},
			{
				"Tab  Enter  Esc",
				"Move between the list and the detail pane",
			},
			{
				"← →  1 2 3 4",
				"General, Files, Peers or Trackers tab",
			},
			{
				"Mouse",
				"Click to choose, wheel to scroll",
			},
		},
	},
	{
		helpTorrents,
		[]helpKey{
			{
				"a",
				"Add a .torrent file or a magnet link",
			},
			{
				"A",
				"Add, choosing the folder to save in",
			},
			{
				"n",
				"Make a torrent from a file or folder",
			},
			{
				"Paste",
				"Pasting a magnet link adds it",
			},
			{
				"Space  p",
				"Pause or resume",
			},
			{
				"r",
				"Check the files on disk again",
			},
			{
				"Delete",
				"Remove the torrent, keeping its files",
			},
			{
				"Shift-Delete  D",
				"Remove the torrent and delete its files",
			},
			{
				"c",
				"Copy the magnet link",
			},
		},
	},
	{
		helpFiles,
		[]helpKey{
			{
				"Space",
				"Download the file or skip it",
			},
			{
				"+  -",
				"Download every file, or skip every file",
			},
		},
	},
	{
		helpFinding,
		[]helpKey{
			{
				"/  Ctrl-F",
				"Filter the list by name; Esc clears it",
			},
		},
	},
	{
		helpCommands,
		[]helpKey{
			{
				"Ctrl-P",
				"Every command, found by typing part of its name",
			},
			{
				"t",
				"Next colour theme",
			},
		},
	},
	{
		helpLeaving,
		[]helpKey{
			{
				"?  F1",
				"This help",
			},
			{
				"q  Ctrl-Q  Ctrl-C",
				"Quit; torrents carry on next time",
			},
		},
	},
}
