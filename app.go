package tunneld

type App struct {
	Name string

	Command string
	Args    []string

	Count int
}

type AppPort struct {
	App  App
	Port int
}
