package config

type Config struct {
    Port string
    Env  string
}

func Load(path string, env string) (*Config, error) {
    return &Config{
        Port: "8080",
        Env:  env,
    }, nil
}
