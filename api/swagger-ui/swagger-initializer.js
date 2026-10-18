window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "../api/openapi.yaml",
    dom_id: "#swagger-ui",
    deepLinking: true,
    displayRequestDuration: true,
    filter: true,
    tryItOutEnabled: true,
    persistAuthorization: false,
    validatorUrl: null,
    supportedSubmitMethods: ["get", "post", "patch"]
  });
});
