package tgf

// H1 平台凭据脱敏登记测试：PlatformConfig 的 Secret 类 env key 必须在
// sensitiveEnvKeys 登记（启动日志快照打印替换为 ******），
// 非凭据类标识（AppID / TeamID / KeyID）不脱敏。

import "testing"

func TestSensitiveEnvKeys_PlatformSecrets(t *testing.T) {
	tests := []struct {
		name          string
		key           Environment
		wantSensitive bool
	}{
		{name: "微信 AppSecret 脱敏", key: EnvironmentWechatAppSecret, wantSensitive: true},
		{name: "TikTok AppSecret 脱敏", key: EnvironmentTiktokAppSecret, wantSensitive: true},
		{name: "Apple 私钥脱敏", key: EnvironmentApplePrivateKey, wantSensitive: true},
		{name: "Facebook AppSecret 脱敏", key: EnvironmentFacebookAppSecret, wantSensitive: true},
		{name: "微信 AppID 不脱敏", key: EnvironmentWechatAppID, wantSensitive: false},
		{name: "TikTok AppID 不脱敏", key: EnvironmentTiktokAppID, wantSensitive: false},
		{name: "Apple TeamID 不脱敏", key: EnvironmentAppleTeamID, wantSensitive: false},
		{name: "Apple KeyID 不脱敏", key: EnvironmentAppleKeyID, wantSensitive: false},
		{name: "Facebook AppID 不脱敏", key: EnvironmentFacebookAppID, wantSensitive: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sensitiveEnvKeys[string(tt.key)]; got != tt.wantSensitive {
				t.Errorf("sensitiveEnvKeys[%v] = %v, want %v", tt.key, got, tt.wantSensitive)
			}
		})
	}
}
