package datahub_servicebroker

import (
	"errors"
	"fmt"
	//marathon "github.com/gambol99/go-marathon"
	//kapi "golang.org/x/build/kubernetes/api"
	//"golang.org/x/build/kubernetes"
	//"golang.org/x/oauth2"
	//"net/http"
	//"net"
	"github.com/pivotal-cf/brokerapi"
	//"time"
	"bytes"
	"encoding/json"
	"strings"
	//"crypto/sha1"
	//"encoding/base64"
	//"text/template"
	//"io"
	"io/ioutil"
	"os"
	//"sync"

	"github.com/pivotal-golang/lager"

	//"k8s.io/kubernetes/pkg/util/yaml"
	routeapi "github.com/openshift/origin/route/api/v1"
	kapi "k8s.io/kubernetes/pkg/api/v1"

	"database/sql"
	oshandler "github.com/asiainfoLDP/datahub_servicebroker/handler"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/s3"
	_ "github.com/go-sql-driver/mysql"
	"net/http"
)

//==============================================================
//
//==============================================================

const DataHubServcieBrokerName_Standalone = "DataHub_standalone"
const S3REGION = "cn-north-1"

func init() {
	oshandler.Register(DataHubServcieBrokerName_Standalone, &DataHub_Handler{})

	logger = lager.NewLogger(DataHubServcieBrokerName_Standalone)
	logger.RegisterSink(lager.NewWriterSink(os.Stdout, lager.DEBUG))
}

var logger lager.Logger

var mysqlDatabase string
var mysqlUser string
var mysqlPassword string

type dpCreate struct {
	Name string `json:"dpname, omitempty"`
	Type string `json:"dptype, omitempty"`
	Conn string `json:"dpconn, omitempty"`
}

//==============================================================
//
//==============================================================
//
//type NiFi_freeHandler struct{}
//
//func (handler *NiFi_freeHandler) DoProvision(instanceID string, details brokerapi.ProvisionDetails, asyncAllowed bool) (brokerapi.ProvisionedServiceSpec, oshandler.ServiceInfo, error) {
//	return newNiFiHandler().DoProvision(instanceID, details, asyncAllowed)
//}
//
//func (handler *NiFi_freeHandler) DoLastOperation(myServiceInfo *oshandler.ServiceInfo) (brokerapi.LastOperation, error) {
//	return newNiFiHandler().DoLastOperation(myServiceInfo)
//}
//
//func (handler *NiFi_freeHandler) DoDeprovision(myServiceInfo *oshandler.ServiceInfo, asyncAllowed bool) (brokerapi.IsAsync, error) {
//	return newNiFiHandler().DoDeprovision(myServiceInfo, asyncAllowed)
//}
//
//func (handler *NiFi_freeHandler) DoBind(myServiceInfo *oshandler.ServiceInfo, bindingID string, details brokerapi.BindDetails) (brokerapi.Binding, oshandler.Credentials, error) {
//	return newNiFiHandler().DoBind(myServiceInfo, bindingID, details)
//}
//
//func (handler *NiFi_freeHandler) DoUnbind(myServiceInfo *oshandler.ServiceInfo, mycredentials *oshandler.Credentials) error {
//	return newNiFiHandler().DoUnbind(myServiceInfo, mycredentials)
//}

//==============================================================
//
//==============================================================

type DataHub_Handler struct{}

//func newNiFiHandler() *NiFi_Handler {
//	return &NiFi_Handler{}
//}

func (handler *DataHub_Handler) DoProvision(instanceID string, details brokerapi.ProvisionDetails, asyncAllowed bool) (brokerapi.ProvisionedServiceSpec, oshandler.ServiceInfo, error) {
	println("DoProvision......")
	println()

	serviceSpec := brokerapi.ProvisionedServiceSpec{IsAsync: asyncAllowed}
	serviceInfo := oshandler.ServiceInfo{}
	serviceSpec.IsAsync = true
	mysqlUrl := oshandler.MysqlAddrPort()

	instanceIdInTempalte := strings.ToLower(oshandler.NewThirteenLengthID())

	serviceBrokerNamespace := oshandler.OC().Namespace()

	var daemonToken string
	paras := details.Parameters
	if v, ok := paras["token"]; ok {
		daemonToken = v.(string)
	}
	println(daemonToken)

	mysqlDatabase = daemonToken
	mysqlUser = instanceIdInTempalte
	mysqlPassword = strings.ToLower(oshandler.NewThirteenLengthID())
	err := createDatabaseForUser(mysqlDatabase, mysqlUser, mysqlPassword)
	if err != nil {
		return serviceSpec, serviceInfo, err
	}
	println("create database done......")
	//=====================================================================================================

	println()
	println("instanceIdInTempalte = ", instanceIdInTempalte)
	println("serviceBrokerNamespace = ", serviceBrokerNamespace)
	println()

	output, err := createDataHubResources(instanceIdInTempalte, serviceBrokerNamespace, daemonToken)
	if err != nil {
		destroyDataHubResources(output, serviceBrokerNamespace)

		return serviceSpec, serviceInfo, err
	}
	println("createDataHubResources done......")
	//=====================================================================================================

	serviceInfo.Url = mysqlUrl
	serviceInfo.Admin_user = "root"
	serviceInfo.Admin_password = oshandler.MysqlPassword()
	serviceInfo.Database = daemonToken // may be not needed
	serviceInfo.User = instanceIdInTempalte
	serviceInfo.Password = mysqlPassword
	serviceInfo.ServiceBrokerNamespace = serviceBrokerNamespace

	serviceSpec.DashboardURL = fmt.Sprintf("http://%s/   mysql://"+
		mysqlUser+":"+
		mysqlPassword+"@"+
		strings.Split(mysqlUrl, ":")[0]+":"+
		strings.Split(mysqlUrl, ":")[1]+"?db=%s", output.route.Spec.Host, mysqlDatabase)

	return serviceSpec, serviceInfo, nil
}

func (handler *DataHub_Handler) DoLastOperation(myServiceInfo *oshandler.ServiceInfo) (brokerapi.LastOperation, error) {

	// assume in provisioning

	// the job may be finished or interrupted or running in another instance.

	datahub_res, _ := getDataHubResources(myServiceInfo.Url, myServiceInfo.Database, myServiceInfo.User)

	//ok := func(rc *kapi.ReplicationController) bool {
	//	if rc == nil || rc.Name == "" || rc.Spec.Replicas == nil || rc.Status.Replicas < *rc.Spec.Replicas {
	//		return false
	//	}
	//	return true
	//}
	ok := func(rc *kapi.ReplicationController) bool {
		if rc == nil || rc.Name == "" || rc.Spec.Replicas == nil || rc.Status.Replicas < *rc.Spec.Replicas {
			return false
		}
		n, _ := statRunningPodsByLabels(myServiceInfo.Database, rc.Labels)
		return n >= *rc.Spec.Replicas
	}

	//println("num_ok_rcs = ", num_ok_rcs)

	// todo: check if http get dashboard request is ok

	if ok(&datahub_res.rc) {
		return brokerapi.LastOperation{
			State:       brokerapi.Succeeded,
			Description: "Succeeded!",
		}, nil
	} else {
		return brokerapi.LastOperation{
			State:       brokerapi.InProgress,
			Description: "In progress.",
		}, nil
	}
}

func (handler *DataHub_Handler) DoDeprovision(myServiceInfo *oshandler.ServiceInfo, asyncAllowed bool) (brokerapi.IsAsync, error) {
	println("DoDeprovision......")

	println("drop databae and user......")
	err := dropDatabaseForUser(myServiceInfo.Url, myServiceInfo.Admin_user, myServiceInfo.Admin_password, myServiceInfo.Database, myServiceInfo.User)
	if err != nil {
		return brokerapi.IsAsync(false), err
	}
	println("drop databae and user done......")
	println("destroy resources......")
	datahub_res, _ := getDataHubResources(myServiceInfo.User, myServiceInfo.ServiceBrokerNamespace, myServiceInfo.Database)
	destroyDataHubResources(datahub_res, myServiceInfo.ServiceBrokerNamespace)
	println("destroy resources done......")

	return brokerapi.IsAsync(false), nil
}

func (handler *DataHub_Handler) DoBind(myServiceInfo *oshandler.ServiceInfo, bindingID string, details brokerapi.BindDetails) (brokerapi.Binding, oshandler.Credentials, error) {
	println("DoBind......")
	println()

	//Creates a new bucket.
	s3Svc := s3.New(session.New(), aws.NewConfig().WithRegion(S3REGION))

	createBucketParams := &s3.CreateBucketInput{
		Bucket: aws.String(myServiceInfo.Database), // Required
	}
	_, err := s3Svc.CreateBucket(createBucketParams)
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}
	println("create bucket done......")
	println()
	//==========================================================================================
	//create a user
	iamSvc := iam.New(session.New(), aws.NewConfig().WithRegion(S3REGION))

	createUserParams := &iam.CreateUserInput{
		UserName: aws.String(myServiceInfo.User), // Required
	}
	_, err = iamSvc.CreateUser(createUserParams)
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}
	println("create a user......")
	println()
	//==========================================================================================
	//create accessKey for user
	//svc := iam.New(session.New(), aws.NewConfig().WithRegion("cn-north-1"))
	//
	createAccessKeyParams := &iam.CreateAccessKeyInput{
		UserName: aws.String(myServiceInfo.User),
	}
	iamResp, err := iamSvc.CreateAccessKey(createAccessKeyParams)
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}
	accessKeyId := iamResp.AccessKey.AccessKeyId
	secretAccessKey := iamResp.AccessKey.SecretAccessKey
	println("create accessKey for user done......")
	println()
	//==========================================================================================
	//Adds or updates an inline policy document that is embedded in the specified IAM user.
	//svc := iam.New(session.New(), aws.NewConfig().WithRegion("cn-north-1"))
	//
	jsonDocument := `{
			    "Statement": [
				{
				    "Effect": "Allow",
				    "Action": [
					"s3:GetObject",
					"s3:PutObject"
				    ],
				    "Resource": [
					"arn:aws-cn:s3:::bucketName/*"
				    ]
				},
				{
				    "Effect": "Allow",
				    "Action": [
					"s3:ListBucket"
				    ],
				    "Resource": [
					"arn:aws-cn:s3:::bucketName"
				    ]
				}
			    ]
			}`
	jsonDocument = strings.Replace(jsonDocument, "bucketName", myServiceInfo.Database, -1)

	println(jsonDocument)
	println()

	putUserPolicyParams := &iam.PutUserPolicyInput{
		PolicyDocument: aws.String(jsonDocument),       // Required
		PolicyName:     aws.String(myServiceInfo.User), // Required
		UserName:       aws.String(myServiceInfo.User), // Required
	}
	_, err = iamSvc.PutUserPolicy(putUserPolicyParams)
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}
	println("create policy done......")
	println()
	//==========================================================================================

	output, err := getDataHubResources(myServiceInfo.User, myServiceInfo.ServiceBrokerNamespace, myServiceInfo.Database)
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}

	dpconn := myServiceInfo.Database + "##" + *accessKeyId + "##" + *secretAccessKey + "##" + S3REGION
	dp := dpCreate{
		Name: "s3dp",
		Type: "s3",
		Conn: dpconn,
	}

	reqBody, err := json.Marshal(dp)
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}

	resp, err := http.Post("http://"+output.route.Spec.Host+"/api/datapools", "application/json;charset=utf-8", bytes.NewBuffer(reqBody))
	if err != nil {
		return brokerapi.Binding{}, oshandler.Credentials{}, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return brokerapi.Binding{}, oshandler.Credentials{}, errors.New(string(b))
	}
	println("create datapool done......")
	println()
	//==========================================================================================

	mycredentials := oshandler.Credentials{
		Name: "s3dp",
		Uri:  "s3://" + dpconn,
	}

	myBinding := brokerapi.Binding{Credentials: mycredentials}

	return myBinding, mycredentials, nil
}

func (handler *DataHub_Handler) DoUnbind(myServiceInfo *oshandler.ServiceInfo, mycredentials *oshandler.Credentials) error {
	// do nothing

	return nil
}

//=======================================================================
//
//=======================================================================

var DataHubTemplateData []byte = nil

func loadDataHubResources(instanceID, daemonToekn string, res *datahubResources) error {
	if DataHubTemplateData == nil {
		f, err := os.Open("datahub.yaml")
		if err != nil {
			return err
		}
		DataHubTemplateData, err = ioutil.ReadAll(f)
		if err != nil {
			return err
		}
		endpoint_postfix := oshandler.EndPointSuffix()
		endpoint_postfix = strings.TrimSpace(endpoint_postfix)
		if len(endpoint_postfix) > 0 {
			DataHubTemplateData = bytes.Replace(
				DataHubTemplateData,
				[]byte("endpoint-postfix-place-holder"),
				[]byte(endpoint_postfix),
				-1)
		}
		datahub_image := oshandler.DatahubImage()
		datahub_image = strings.TrimSpace(datahub_image)
		if len(datahub_image) > 0 {
			DataHubTemplateData = bytes.Replace(
				DataHubTemplateData,
				[]byte("http://datahub-image-place-holder/nifi-openshift-orchestration"),
				[]byte(datahub_image),
				-1)
		}
	}

	mysqlAddrPort := oshandler.MysqlAddrPort()
	mysqlAddr := strings.Split(mysqlAddrPort, ":")[0]
	//mysqlPort := strings.Split(mysqlAddrPort, ":")[1]
	//port := strconv.Itoa(mysqlPort)

	// ...
	yamlTemplates := DataHubTemplateData
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("instanceid"), []byte(instanceID), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("daemon-token-replace"), []byte(daemonToekn), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("mysql-addr-replace"), []byte(mysqlAddr), -1)
	//yamlTemplates = bytes.Replace(yamlTemplates, []byte("mysql-port-replace"), []byte(mysqlPort), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("mysql-database-replace"), []byte(mysqlDatabase), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("mysql-user-replace"), []byte(mysqlUser), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("mysql-password-replace"), []byte(mysqlPassword), -1)

	//println("========= Boot yamlTemplates ===========")
	//println(string(yamlTemplates))
	//println()

	decoder := oshandler.NewYamlDecoder(yamlTemplates)
	decoder.
		Decode(&res.rc).
		Decode(&res.route).
		Decode(&res.route2).
		Decode(&res.service)

	return decoder.Err
}

type datahubResources struct {
	rc      kapi.ReplicationController
	route   routeapi.Route
	route2  routeapi.Route
	service kapi.Service
}

func createDataHubResources(instanceId, serviceBrokerNamespace, daemonToken string) (*datahubResources, error) {

	println("createDataHubResources......")

	var input datahubResources
	err := loadDataHubResources(instanceId, daemonToken, &input)
	if err != nil {
		return nil, err
	}

	var output datahubResources

	osr := oshandler.NewOpenshiftREST(oshandler.OC())

	// here, not use job.post
	prefix := "/namespaces/" + serviceBrokerNamespace
	osr.
		KPost(prefix+"/replicationcontrollers", &input.rc, &output.rc).
		OPost(prefix+"/routes", &input.route, &output.route).
		OPost(prefix+"/routes", &input.route2, &output.route2).
		KPost(prefix+"/services", &input.service, &output.service)

	if osr.Err != nil {
		logger.Error("createDataHubResources", osr.Err)
	}

	return &output, osr.Err
}

func getDataHubResources(instanceId, serviceBrokerNamespace, daemonToken string) (*datahubResources, error) {
	var output datahubResources

	var input datahubResources
	err := loadDataHubResources(instanceId, daemonToken, &input)
	if err != nil {
		return &output, err
	}

	osr := oshandler.NewOpenshiftREST(oshandler.OC())

	prefix := "/namespaces/" + serviceBrokerNamespace
	osr.
		KGet(prefix+"/replicationcontrollers/"+input.rc.Name, &output.rc).
		OGet(prefix+"/routes/"+input.route.Name, &output.route).
		OGet(prefix+"/routes/"+input.route2.Name, &output.route2).
		KGet(prefix+"/services/"+input.service.Name, &output.service)

	if osr.Err != nil {
		logger.Error("getDataHubResources", osr.Err)
	}

	return &output, osr.Err
}

func destroyDataHubResources(datahubRes *datahubResources, serviceBrokerNamespace string) {
	// todo: add to retry queue on fail

	go func() { kdel_rc(serviceBrokerNamespace, &datahubRes.rc) }()
	go func() { odel(serviceBrokerNamespace, "routes", datahubRes.route.Name) }()
	go func() { odel(serviceBrokerNamespace, "routes", datahubRes.route2.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", datahubRes.service.Name) }()
}

//===============================================================
//
//===============================================================

func kpost(serviceBrokerNamespace, typeName string, body interface{}, into interface{}) error {
	println("to create ", typeName)

	uri := fmt.Sprintf("/namespaces/%s/%s", serviceBrokerNamespace, typeName)
	i, n := 0, 5
RETRY:

	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KPost(uri, body, into)
	if osr.Err == nil {
		logger.Info("create " + typeName + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> create (%s) error", i, typeName), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("create (%s) failed", typeName), osr.Err)
			return osr.Err
		}
	}

	return nil
}

func opost(serviceBrokerNamespace, typeName string, body interface{}, into interface{}) error {
	println("to create ", typeName)

	uri := fmt.Sprintf("/namespaces/%s/%s", serviceBrokerNamespace, typeName)
	i, n := 0, 5
RETRY:

	osr := oshandler.NewOpenshiftREST(oshandler.OC()).OPost(uri, body, into)
	if osr.Err == nil {
		logger.Info("create " + typeName + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> create (%s) error", i, typeName), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("create (%s) failed", typeName), osr.Err)
			return osr.Err
		}
	}

	return nil
}

func kdel(serviceBrokerNamespace, typeName, resName string) error {
	if resName == "" {
		return nil
	}

	println("to delete ", typeName, "/", resName)

	uri := fmt.Sprintf("/namespaces/%s/%s/%s", serviceBrokerNamespace, typeName, resName)
	i, n := 0, 5
RETRY:
	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KDelete(uri, nil)
	if osr.Err == nil {
		logger.Info("delete " + uri + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> delete (%s) error", i, uri), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("delete (%s) failed", uri), osr.Err)
			return osr.Err
		}
	}

	return nil
}

func odel(serviceBrokerNamespace, typeName, resName string) error {
	if resName == "" {
		return nil
	}

	println("to delete ", typeName, "/", resName)

	uri := fmt.Sprintf("/namespaces/%s/%s/%s", serviceBrokerNamespace, typeName, resName)
	i, n := 0, 5
RETRY:
	osr := oshandler.NewOpenshiftREST(oshandler.OC()).ODelete(uri, nil)
	if osr.Err == nil {
		logger.Info("delete " + uri + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> delete (%s) error", i, uri), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("delete (%s) failed", uri), osr.Err)
			return osr.Err
		}
	}

	return nil
}

/*
func kdel_rc (serviceBrokerNamespace string, rc *kapi.ReplicationController) {
	kdel (serviceBrokerNamespace, "replicationcontrollers", rc.Name)
}
*/

func kdel_rc(serviceBrokerNamespace string, rc *kapi.ReplicationController) {
	// looks pods will be auto deleted when rc is deleted.

	if rc == nil || rc.Name == "" {
		return
	}

	println("to delete pods on replicationcontroller", rc.Name)

	uri := "/namespaces/" + serviceBrokerNamespace + "/replicationcontrollers/" + rc.Name

	// modfiy rc replicas to 0

	zero := 0
	rc.Spec.Replicas = &zero
	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KPut(uri, rc, nil)
	if osr.Err != nil {
		logger.Error("modify HA rc", osr.Err)
		return
	}

	// start watching rc status

	statuses, cancel, err := oshandler.OC().KWatch(uri)
	if err != nil {
		logger.Error("start watching HA rc", err)
		return
	}

	go func() {
		for {
			status, _ := <-statuses

			if status.Err != nil {
				logger.Error("watch HA nifi rc error", status.Err)
				close(cancel)
				return
			} else {
				//logger.Debug("watch nifi HA rc, status.Info: " + string(status.Info))
			}

			var wrcs watchReplicationControllerStatus
			if err := json.Unmarshal(status.Info, &wrcs); err != nil {
				logger.Error("parse master HA rc status", err)
				close(cancel)
				return
			}

			if wrcs.Object.Status.Replicas <= 0 {
				break
			}
		}

		// ...

		kdel(serviceBrokerNamespace, "replicationcontrollers", rc.Name)
	}()

	return
}

type watchReplicationControllerStatus struct {
	// The type of watch update contained in the message
	Type string `json:"type"`
	// RC details
	Object kapi.ReplicationController `json:"object"`
}

func statRunningPodsByLabels(serviceBrokerNamespace string, labels map[string]string) (int, error) {

	println("to list pods in", serviceBrokerNamespace)

	uri := "/namespaces/" + serviceBrokerNamespace + "/pods"

	pods := kapi.PodList{}

	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KList(uri, labels, &pods)
	if osr.Err != nil {
		return 0, osr.Err
	}

	nrunnings := 0

	for i := range pods.Items {
		pod := &pods.Items[i]

		println("\n pods.Items[", i, "].Status.Phase =", pod.Status.Phase, "\n")

		if pod.Status.Phase == kapi.PodRunning {
			nrunnings++
		}
	}

	return nrunnings, nil
}

func createDatabaseForUser(dbname, user, password string) error {

	println("create database......")

	mysqlAdminPassword := oshandler.MysqlPassword()
	mysqlUsername := oshandler.MysqlUser()
	mysqlUrl := oshandler.MysqlAddrPort()

	println(mysqlAdminPassword)
	println(mysqlUrl)

	db, err := sql.Open("mysql", ""+mysqlUsername+":"+mysqlAdminPassword+"@tcp("+mysqlUrl+")/")
	if err != nil {
		return err
	}
	//测试是否能联通
	err = db.Ping()
	if err != nil {
		return err
	}
	fmt.Println(db)
	defer db.Close()

	_, err = db.Query("CREATE DATABASE " + dbname + " DEFAULT CHARACTER SET utf8 COLLATE utf8_general_ci")
	if err != nil {
		return err
	}

	_, err = db.Query("GRANT ALL ON " + dbname + ".* TO '" + user + "'@'%' IDENTIFIED BY '" + password + "'")
	if err != nil {
		return err
	}

	return err
}

func dropDatabaseForUser(url, adminUser, adminPassword, database, user string) error {
	db, err := sql.Open("mysql", adminUser+":"+adminPassword+"@tcp("+url+")/")

	if err != nil {
		return err
	}

	err = db.Ping()

	if err != nil {
		return err
	}

	defer db.Close()

	//删除数据库
	_, err = db.Query("DROP DATABASE " + database)

	if err != nil {
		return err
	}

	//删除用户
	_, err = db.Query("DROP USER " + user)

	if err != nil {
		return err
	}

	return err
}
