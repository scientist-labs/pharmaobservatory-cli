package fixture

// Synthetic SDK source shaped like generated code. It is parsed, never compiled.

type WidgetsAPIService service

type ApiWidgetsListRequest struct {
	ctx        context.Context
	ApiService *WidgetsAPIService
	cursor     *string
	limit      *int32
	json       *bool
	ratio      *float32
}

func (r ApiWidgetsListRequest) Cursor(cursor string) ApiWidgetsListRequest {
	r.cursor = &cursor
	return r
}

func (r ApiWidgetsListRequest) Limit(limit int32) ApiWidgetsListRequest {
	r.limit = &limit
	return r
}

func (r ApiWidgetsListRequest) Json(json bool) ApiWidgetsListRequest {
	r.json = &json
	return r
}

func (r ApiWidgetsListRequest) Ratio(ratio float32) ApiWidgetsListRequest {
	r.ratio = &ratio
	return r
}

func (r ApiWidgetsListRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.WidgetsListExecute(r)
}

func (a *WidgetsAPIService) WidgetsList(ctx context.Context) ApiWidgetsListRequest {
	return ApiWidgetsListRequest{ApiService: a, ctx: ctx}
}

func (a *WidgetsAPIService) WidgetsListExecute(r ApiWidgetsListRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/widgets"
	if r.cursor != nil {
		parameterAddToHeaderOrQuery(localVarQueryParams, "cursor", r.cursor, "form", "")
	}
	if r.limit != nil {
		parameterAddToHeaderOrQuery(localVarQueryParams, "limit", r.limit, "form", "")
	}
	if r.json != nil {
		parameterAddToHeaderOrQuery(localVarQueryParams, "json", r.json, "form", "")
	}
	if r.ratio != nil {
		parameterAddToHeaderOrQuery(localVarQueryParams, "ratio", r.ratio, "form", "")
	}
	return nil, nil, nil
}

type ApiWidgetsGetRequest struct {
	ctx        context.Context
	ApiService *WidgetsAPIService
	id         string
}

func (r ApiWidgetsGetRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.WidgetsGetExecute(r)
}

func (a *WidgetsAPIService) WidgetsGet(ctx context.Context, id string) ApiWidgetsGetRequest {
	return ApiWidgetsGetRequest{ApiService: a, ctx: ctx, id: id}
}

func (a *WidgetsAPIService) WidgetsGetExecute(r ApiWidgetsGetRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/widgets/{id}"
	return nil, nil, nil
}

type ApiWidgetsPartsRequest struct {
	ctx        context.Context
	ApiService *WidgetsAPIService
	id         string
	colour     *string
}

func (r ApiWidgetsPartsRequest) Colour(colour string) ApiWidgetsPartsRequest {
	r.colour = &colour
	return r
}

func (r ApiWidgetsPartsRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.WidgetsPartsExecute(r)
}

func (a *WidgetsAPIService) WidgetsParts(ctx context.Context, id string) ApiWidgetsPartsRequest {
	return ApiWidgetsPartsRequest{ApiService: a, ctx: ctx, id: id}
}

func (a *WidgetsAPIService) WidgetsPartsExecute(r ApiWidgetsPartsRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/widgets/{id}/parts"
	if r.colour != nil {
		parameterAddToHeaderOrQuery(localVarQueryParams, "colour", r.colour, "form", "")
	}
	return nil, nil, nil
}

type ApiWidgetsCountsRequest struct {
	ctx        context.Context
	ApiService *WidgetsAPIService
}

func (r ApiWidgetsCountsRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.WidgetsCountsExecute(r)
}

func (a *WidgetsAPIService) WidgetsCounts(ctx context.Context) ApiWidgetsCountsRequest {
	return ApiWidgetsCountsRequest{ApiService: a, ctx: ctx}
}

func (a *WidgetsAPIService) WidgetsCountsExecute(r ApiWidgetsCountsRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/widgets/counts"
	return nil, nil, nil
}

type ApiWidgetsHelpRequest struct {
	ctx        context.Context
	ApiService *WidgetsAPIService
}

func (r ApiWidgetsHelpRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.WidgetsHelpExecute(r)
}

func (a *WidgetsAPIService) WidgetsHelp(ctx context.Context) ApiWidgetsHelpRequest {
	return ApiWidgetsHelpRequest{ApiService: a, ctx: ctx}
}

func (a *WidgetsAPIService) WidgetsHelpExecute(r ApiWidgetsHelpRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/widgets/help"
	return nil, nil, nil
}

type LoginAPIService service

type ApiLoginListRequest struct {
	ctx        context.Context
	ApiService *LoginAPIService
}

func (r ApiLoginListRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.LoginListExecute(r)
}

func (a *LoginAPIService) LoginList(ctx context.Context) ApiLoginListRequest {
	return ApiLoginListRequest{ApiService: a, ctx: ctx}
}

func (a *LoginAPIService) LoginListExecute(r ApiLoginListRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/login"
	return nil, nil, nil
}

type ApiV1BaseAPIService service

type ApiApiV1WhoamiGetRequest struct {
	ctx        context.Context
	ApiService *ApiV1BaseAPIService
}

func (r ApiApiV1WhoamiGetRequest) Execute() (*X, *http.Response, error) {
	return r.ApiService.ApiV1WhoamiGetExecute(r)
}

func (a *ApiV1BaseAPIService) ApiV1WhoamiGet(ctx context.Context) ApiApiV1WhoamiGetRequest {
	return ApiApiV1WhoamiGetRequest{ApiService: a, ctx: ctx}
}

func (a *ApiV1BaseAPIService) ApiV1WhoamiGetExecute(r ApiApiV1WhoamiGetRequest) (*X, *http.Response, error) {
	var (
		localVarHTTPMethod = http.MethodGet
	)
	localVarPath := localBasePath + "/api/v1/whoami"
	return nil, nil, nil
}
