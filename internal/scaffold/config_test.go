package scaffold

import "testing"

func TestConfigMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "single defaults", cfg: Config{Module: "example.com/single", Dir: "single"}},
		{name: "single all redis mysql", cfg: Config{Module: "example.com/single", Dir: "single", Profile: "single", Data: "redis-mysql", Protocol: "all"}},
		{name: "distributed defaults redis", cfg: Config{Module: "example.com/dist", Dir: "dist", Profile: "distributed"}},
		{name: "distributed rejects none", cfg: Config{Module: "example.com/dist", Dir: "dist", Profile: "distributed", Data: "none", Protocol: "tcp"}, wantErr: true},
		{name: "http rest", cfg: Config{Module: "example.com/rest", Dir: "rest", Profile: "http-rest"}},
		{name: "http rpc", cfg: Config{Module: "example.com/rpc", Dir: "rpc", Profile: "http-rpc", Data: "redis"}},
		{name: "http rejects gateway", cfg: Config{Module: "example.com/rest", Dir: "rest", Profile: "http-rest", Protocol: "tcp"}, wantErr: true},
		{name: "game rejects none protocol", cfg: Config{Module: "example.com/game", Dir: "game", Profile: "single", Protocol: "none"}, wantErr: true},
		{name: "invalid deploy", cfg: Config{Module: "example.com/game", Dir: "game", Deploy: "prod"}, wantErr: true},
		{name: "invalid module path", cfg: Config{Module: "example.com//game", Dir: "game"}, wantErr: true},
		{name: "missing module", cfg: Config{Dir: "game"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
