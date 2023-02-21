package log

import (
	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"os"
)

//***************************************************
//author tim.huang
//2023/2/21
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

// ***********************    var    ****************************

var logger zap.Logger
var logger_level = 3

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func initLogger() {

	env := os.Getenv("GODAILYLIB_ENV")
	if env == "" {
		env = "development"
	}

	err := godotenv.Load(".env." + env)
	if err != nil {

	}

	err = godotenv.Load()
	if err != nil {

	}

	var (
	//cfg zap.Config
	)
	//cfg = zap.NewProductionConfig()

}

func init() {
	initLogger()
}
