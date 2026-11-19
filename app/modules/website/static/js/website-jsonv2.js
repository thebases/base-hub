;(function (global) {
  "use strict"

  var DEFAULT_SEARCH_READ_LIMIT = 80
  var REQUEST_TIMEOUT_MS = 15000
  var DEFAULT_RPC_DB_NAME = ""
  var RPC_DB_COOKIE_NAME = "basehub_db_name"
  var RPC_DB_LOCK_COOKIE_NAME = "basehub_db_locked"
  var AUTH_INVALID_EVENT = "basehub:auth-invalid"
  var useOidc = false
  var currentRpcAccessToken = ""
  var currentRpcDatabaseName = readPinnedRpcDatabaseName() || DEFAULT_RPC_DB_NAME
  var currentRpcSessionContext = {}
  var authEventEmitted = false

  /** Returns a shallow clone so request kwargs/context are never mutated in place. */
  function clone(value) {
    if (Array.isArray(value)) {
      return value.slice()
    }
    if (value && typeof value === "object") {
      return Object.assign({}, value)
    }
    return value
  }

  /** Reads a browser cookie value by name, returning an empty string when absent. */
  function readCookie(name) {
    if (!global.document) {
      return ""
    }
    var prefix = name + "="
    var parts = global.document.cookie.split("; ")
    for (var index = 0; index < parts.length; index += 1) {
      if (parts[index].indexOf(prefix) === 0) {
        return decodeURIComponent(parts[index].slice(prefix.length))
      }
    }
    return ""
  }

  /** Returns the tenant database pinned by backend cookies, if pinning is active. */
  function readPinnedRpcDatabaseName() {
    if (readCookie(RPC_DB_LOCK_COOKIE_NAME) !== "1") {
      return ""
    }
    return readCookie(RPC_DB_COOKIE_NAME).trim()
  }

  /** Reads the admin SPA persisted auth snapshot for pages sharing the same origin. */
  function readPersistedAuthState() {
    if (!global.sessionStorage) {
      return {}
    }
    var raw = global.sessionStorage.getItem("basehub-auth")
    if (!raw) {
      return {}
    }
    try {
      var parsed = JSON.parse(raw)
      var state = parsed && parsed.state ? parsed.state : {}
      return {
        accessToken: typeof state.accessToken === "string" ? state.accessToken : "",
        db: typeof state.db === "string" ? state.db : "",
      }
    } catch (_error) {
      return {}
    }
  }

  /** Parses JSON response bodies and falls back to plain text for non-JSON errors. */
  function readResponsePayload(response) {
    return response.text().then(function (text) {
      if (!text) {
        return null
      }
      try {
        return JSON.parse(text)
      } catch (_error) {
        return text
      }
    })
  }

  /** Converts arbitrary response payloads into text for auth/error matching. */
  function responsePayloadText(payload) {
    if (typeof payload === "string") {
      return payload
    }
    try {
      return JSON.stringify(payload)
    } catch (_error) {
      return ""
    }
  }

  /** Detects backend guidance that an object method still expects ids in args. */
  function shouldFallbackFromPathID(error) {
    var message = error instanceof Error ? error.message : String(error || "")
    return message.indexOf('uses /rpc/{model}/{method}, not /rpc/{model}/{method}/{id}') !== -1
  }

  /** Mirrors the admin SPA invalid-session rule for 401/403 RPC responses. */
  function shouldInvalidateSession(response, payload) {
    if (response.status === 403) {
      return true
    }
    return (
      response.status === 401 &&
      responsePayloadText(payload).toLowerCase().indexOf("invalid basic auth credentials") !== -1
    )
  }

  /** Emits one invalid-auth event so host pages can redirect or clear UI state. */
  function emitInvalidAuthEvent() {
    if (!global.dispatchEvent || authEventEmitted) {
      return
    }
    authEventEmitted = true
    global.dispatchEvent(new CustomEvent(AUTH_INVALID_EVENT))
  }

  /** Resolves the request token lazily so early-created clients survive hydration. */
  function resolveRpcAccessToken(instanceAccessToken) {
    if (currentRpcAccessToken) {
      return currentRpcAccessToken
    }
    if (instanceAccessToken) {
      return instanceAccessToken
    }
    var persistedAuth = readPersistedAuthState()
    var accessToken = persistedAuth.accessToken || ""
    if (accessToken) {
      setRpcToken(accessToken)
    }
    return accessToken
  }

  /** Resolves the active database from explicit state, pinned cookies, or storage. */
  function resolveRpcDatabaseName() {
    var persistedAuth = currentRpcDatabaseName ? {} : readPersistedAuthState()
    var dbName =
      currentRpcDatabaseName ||
      readPinnedRpcDatabaseName() ||
      persistedAuth.db ||
      DEFAULT_RPC_DB_NAME
    if (dbName && dbName !== currentRpcDatabaseName) {
      setRpcDatabase(dbName)
    }
    return dbName
  }

  /** Builds standard JSON-RPC headers with Basic/Bearer auth and tenant DB. */
  function rpcHeaders(accessToken) {
    var headers = {
      "Content-Type": "application/json",
    }
    if (accessToken) {
      headers.Authorization = (useOidc ? "Bearer" : "Basic") + " " + accessToken
    }
    var dbName = resolveRpcDatabaseName()
    if (dbName) {
      headers["X-Database-Name"] = dbName
    }
    return headers
  }

  /** Resolves a backend path against the configured base URL. */
  function buildBackendURL(path, baseURL) {
    return new URL(path, baseURL || global.location.origin).toString()
  }

  /** Exposes active auth headers for non-RPC fetch calls such as file uploads. */
  function authenticatedHeaders(headers) {
    var accessToken = resolveRpcAccessToken("")
    var baseHeaders = accessToken ? rpcHeaders(accessToken) : { "Content-Type": "application/json" }
    return Object.assign(baseHeaders, headers || {})
  }

  /** Adds the current session context and default list limit to method kwargs. */
  function prepareKwargs(method, kwargs) {
    var next = clone(kwargs || {})
    var context = Object.assign({}, currentRpcSessionContext, next.context || {})
    if (Object.keys(context).length > 0) {
      next.context = context
    }
    if (method === "search_read" && next.limit === undefined) {
      next.limit = DEFAULT_SEARCH_READ_LIMIT
    }
    return next
  }

  /** Creates a model-scoped JSON-RPC client compatible with BaseJsonV2Rpc. */
  function BaseJsonV2Rpc(model, options) {
    options = options || {}
    this.model = model
    this.basePath = options.basePath || "/rpc"
    this.baseURL = options.baseURL || global.location.origin
    this.accessToken = options.accessToken || ""
  }

  BaseJsonV2Rpc.prototype.post = function (url, body) {
    var controller = new AbortController()
    var timeoutID = global.setTimeout(function () {
      controller.abort()
    }, REQUEST_TIMEOUT_MS)
    var self = this
    var accessToken = resolveRpcAccessToken(this.accessToken)
    if (accessToken !== this.accessToken) {
      this.setAccessToken(accessToken)
    }
    return fetch(buildBackendURL(url, this.baseURL), {
      method: "POST",
      headers: rpcHeaders(accessToken),
      body: JSON.stringify(body || {}),
      signal: controller.signal,
    })
      .then(function (response) {
        return readResponsePayload(response).then(function (data) {
          if (!response.ok) {
            if (shouldInvalidateSession(response, data)) {
              emitInvalidAuthEvent()
            }
            throw new Error("Response error: " + response.status + " - " + JSON.stringify(data))
          }
          if (data && data.error) {
            var err = new Error(data.error.message)
            err.code = data.error.code
            throw err
          }
          if (!data) {
            throw new Error("Response error: empty response body")
          }
          return data
        })
      })
      .catch(function (error) {
        if (error && error.name === "AbortError") {
          throw new Error("No response received from server")
        }
        if (error instanceof Error && error.name !== "TypeError") {
          throw error
        }
        throw new Error("Request setup error: " + (error && error.message ? error.message : String(error)))
      })
      .finally(function () {
        global.clearTimeout(timeoutID)
        self.setAccessToken(accessToken)
      })
  }

  BaseJsonV2Rpc.prototype.get = function (url) {
    var controller = new AbortController()
    var timeoutID = global.setTimeout(function () {
      controller.abort()
    }, REQUEST_TIMEOUT_MS)
    var accessToken = resolveRpcAccessToken(this.accessToken)
    if (accessToken !== this.accessToken) {
      this.setAccessToken(accessToken)
    }
    return fetch(buildBackendURL(url, this.baseURL), {
      method: "GET",
      headers: rpcHeaders(accessToken),
      signal: controller.signal,
    })
      .then(function (response) {
        return readResponsePayload(response).then(function (data) {
          if (!response.ok) {
            if (shouldInvalidateSession(response, data)) {
              emitInvalidAuthEvent()
            }
            throw new Error("Response error: " + response.status + " - " + JSON.stringify(data))
          }
          if (!data) {
            throw new Error("Response error: empty response body")
          }
          return data
        })
      })
      .catch(function (error) {
        if (error && error.name === "AbortError") {
          throw new Error("No response received from server")
        }
        if (error instanceof Error && error.name !== "TypeError") {
          throw error
        }
        throw new Error("Request setup error: " + (error && error.message ? error.message : String(error)))
      })
      .finally(function () {
        global.clearTimeout(timeoutID)
      })
  }

  BaseJsonV2Rpc.prototype.call = function (method, args, kwargs) {
    return this.post(this.basePath + "/" + this.model + "/" + method, {
      args: args || [],
      kwargs: prepareKwargs(method, kwargs || {}),
    })
  }

  BaseJsonV2Rpc.prototype.callById = function (method, id, args, kwargs) {
    return this.post(this.basePath + "/" + this.model + "/" + method + "/" + id, {
      args: args || [],
      kwargs: prepareKwargs(method, kwargs || {}),
    })
  }

  BaseJsonV2Rpc.prototype.callByIdFirst = function (method, id, args, fallbackArgs, kwargs) {
    var self = this
    return this.callById(method, id, args || [], kwargs || {}).catch(function (error) {
      if (!shouldFallbackFromPathID(error)) {
        throw error
      }
      return self.call(method, fallbackArgs || args || [], kwargs || {})
    })
  }

  BaseJsonV2Rpc.prototype.callDb = function (method, params, id) {
    return this.post("/rpc/db/" + method, { id: id, params: params || {} })
  }

  BaseJsonV2Rpc.prototype.callMeta = function (method, params, id) {
    return this.post("/rpc_meta/model/" + method, { id: id, params: params || {} })
  }

  BaseJsonV2Rpc.prototype.getRpcRoot = function () {
    return this.get(this.basePath)
  }

  BaseJsonV2Rpc.prototype.search = function (domain, fields, order, offset, limit) {
    var kwargs = {
      domain: domain || [],
      fields: fields || ["id", "name"],
    }
    if (order) {
      kwargs.order = order
    }
    if (limit !== undefined) {
      kwargs.limit = limit
    }
    if (offset !== undefined) {
      kwargs.offset = offset
    }
    return this.call("search_read", [], kwargs)
  }

  BaseJsonV2Rpc.prototype.readGroup = function (domain, fields, groupby, orderby, offset, limit) {
    var kwargs = {
      domain: domain || [],
      fields: fields || [],
      groupby: groupby || "",
    }
    if (orderby) {
      kwargs.orderby = orderby
    }
    if (limit !== undefined) {
      kwargs.limit = limit
    }
    if (offset !== undefined) {
      kwargs.offset = offset
    }
    return this.call("read_group", [], kwargs)
  }

  BaseJsonV2Rpc.prototype.create = function (vals) {
    return this.call("create", [vals || {}])
  }

  BaseJsonV2Rpc.prototype.createValList = function (valsList) {
    return this.call("create", [valsList || []])
  }

  BaseJsonV2Rpc.prototype.write = function (ids, vals) {
    var pathID = ids.length === 1 ? ids[0] : ids.join(",")
    return this.callByIdFirst("write", pathID, [vals || {}], [ids, vals || {}])
  }

  BaseJsonV2Rpc.prototype.writeValue = function (ids, values) {
    return this.call("write", [], { ids: ids || [], values: values || {} })
  }

  BaseJsonV2Rpc.prototype.delete = function (ids) {
    var pathID = ids.length === 1 ? ids[0] : ids.join(",")
    return this.callByIdFirst("unlink", pathID, [], [ids])
  }

  BaseJsonV2Rpc.prototype.read = function (ids, fields) {
    var pathID = ids.length === 1 ? ids[0] : ids.join(",")
    return this.callByIdFirst("read", pathID, [fields || []], [ids, fields || []])
  }

  BaseJsonV2Rpc.prototype.setActive = function (ids, active) {
    return this.call("write", [ids || [], { active: !!active }])
  }

  BaseJsonV2Rpc.prototype.run = function (ids, method, vals) {
    return this.call(method, [ids || []], vals || {})
  }

  BaseJsonV2Rpc.prototype.checkAccessToken = function () {
    return !!this.accessToken
  }

  BaseJsonV2Rpc.prototype.setAccessToken = function (accessToken) {
    this.accessToken = accessToken || ""
  }

  /** Authenticates and stores the returned API key token/database for later calls. */
  function authenticate(params, options) {
    options = options || {}
    var effectiveDBName = params.db || readPinnedRpcDatabaseName() || currentRpcDatabaseName
    var headers = { "Content-Type": "application/json" }
    if (effectiveDBName) {
      headers["X-Database-Name"] = effectiveDBName
    }
    var body = {
      params: Object.assign({}, params, { db: effectiveDBName }),
    }
    return fetch(buildBackendURL("/oauth/token", options.baseURL), {
      method: "POST",
      headers: headers,
      body: JSON.stringify(body),
    }).then(function (response) {
      if (!response.ok) {
        return response.text().then(function (text) {
          throw new Error("Authentication failed: " + response.status + " - " + text)
        })
      }
      return response.json().then(function (payload) {
        if (payload && payload.result) {
          if (payload.result.api_key) {
            setRpcToken(payload.result.api_key)
          }
          if (payload.result.db) {
            setRpcDatabase(payload.result.db)
          }
          if (payload.result.user_context) {
            setRpcSessionContext(payload.result.user_context)
          }
        }
        return payload
      })
    })
  }

  /** Checks whether a token/database pair is still valid for the backend session. */
  function getSessionInfo(accessToken, db, options) {
    options = options || {}
    var headers = rpcHeaders(accessToken)
    if (db) {
      headers["X-Database-Name"] = db
    }
    return fetch(buildBackendURL("/oauth/me", options.baseURL), {
      method: "GET",
      headers: headers,
    }).then(function (response) {
      return readResponsePayload(response).then(function (data) {
        if (!response.ok) {
          if (shouldInvalidateSession(response, data)) {
            emitInvalidAuthEvent()
          }
          throw new Error("Session info failed: " + response.status)
        }
        return data
      })
    })
  }

  /** Creates a new model-scoped client using the latest shared token/database state. */
  function createModelRpc(model, options) {
    var client = new BaseJsonV2Rpc(model, options || {})
    client.setAccessToken(currentRpcAccessToken)
    return client
  }

  function setRpcToken(accessToken) {
    currentRpcAccessToken = accessToken || ""
    if (currentRpcAccessToken) {
      authEventEmitted = false
    }
  }

  function setRpcDatabase(dbName) {
    currentRpcDatabaseName = dbName || DEFAULT_RPC_DB_NAME
  }

  function setRpcSessionContext(context) {
    currentRpcSessionContext = clone(context || {})
  }

  global.BaseHubJsonV2 = {
    AUTH_INVALID_EVENT: AUTH_INVALID_EVENT,
    BaseJsonV2Rpc: BaseJsonV2Rpc,
    authenticate: authenticate,
    authenticatedHeaders: authenticatedHeaders,
    buildBackendURL: buildBackendURL,
    createModelRpc: createModelRpc,
    emitInvalidAuthEvent: emitInvalidAuthEvent,
    getPinnedRpcDatabaseName: readPinnedRpcDatabaseName,
    getSessionInfo: getSessionInfo,
    isRpcDatabasePinned: function () {
      return readPinnedRpcDatabaseName() !== ""
    },
    readResponsePayload: readResponsePayload,
    setRpcDatabase: setRpcDatabase,
    setRpcSessionContext: setRpcSessionContext,
    setRpcToken: setRpcToken,
  }
})(window)
