package tframework

//***************************************************
//author tim.huang
//2022/11/4
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

type ILogService interface {
	Info(format string, v ...interface{})

	Debug(format string, v ...interface{})

	Warning(format string, v ...interface{})

	WarningS(format string, v ...interface{})

	InfoS(format string, v ...interface{})

	DebugS(format string, v ...interface{})
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func init() {
}
