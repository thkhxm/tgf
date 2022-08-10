package test

type TestServer struct {
}

func (receiver *TestServer) GetModuleName() (moduleName string) {
	return "test"
}

func (receiver *TestServer) RPCFindBooks() {

}

func (receiver *TestServer) RPcFindApple() {

}
func (receiver *TestServer) SayRed() {

}
