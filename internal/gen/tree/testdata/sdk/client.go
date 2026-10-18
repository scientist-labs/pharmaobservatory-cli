package fixture

type APIClient struct {
	ApiV1BaseAPI *ApiV1BaseAPIService

	WidgetsAPI *WidgetsAPIService

	LoginAPI *LoginAPIService
}
