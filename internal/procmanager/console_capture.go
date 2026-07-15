package procmanager

type childConsole struct {
	started func()
	abort   func()
	finish  func() error
}
