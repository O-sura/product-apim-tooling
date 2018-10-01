/*
*  Copyright (c)
*  WSO2 Inc. licenses this file to you under the Apache License,
*  Version 2.0 (the "License"); you may not use this file except
*  in compliance with the License.
*  You may obtain a copy of the License at
*
*    http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing,
* software distributed under the License is distributed on an
* "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
* KIND, either express or implied.  See the License for the
* specific language governing permissions and limitations
* under the License.
 */

package utils

import (
	"fmt"
	"os"
	"github.com/go-resty/resty"
)

func HandleErrorAndExit(msg string, err error) {
	/*
	fmt.Println("\n=======  DEBUG LOG ==================")
	// TODO:: Remove debug log in production
	for i := 1; i <= 6; i++ {
		fmt.Println(WhereAmI(i))
	}
	fmt.Println("=======  END OF DEBUG LOG ===========\n")
	*/
	if err == nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ProjectName, msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s: %v Reason: %v\n", ProjectName, msg, err.Error())
		Logln(LogPrefixError + msg + ": " + err.Error())
	}
	defer printAndExit()
}

func printAndExit() {
	fmt.Println("Exit status 1")
	os.Exit(1)
}

// Log information of erroneous http response and exit program
func PrintErrorResponseAndExit(response *resty.Response) {
	fmt.Printf("\nResponse Status: %v. %v \n", response.Status(),response)
	Logf("\nResponse Headers: %v", response.Header())
	Logf("\nResponse Time:%v", response.Time())
	Logf("\nResponse Received At:%v", response.ReceivedAt())
	printAndExit()
}