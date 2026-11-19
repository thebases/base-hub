// Registered by the SPA module loader (modules/module-registry.js) so the base bundle never names
// the workflow module. Reached from the "MQTT Monitor" entry in modules/base-workflow/views/menu-base-workflow.xml.
export default {
  routes: {
    "/web/workflow/mqtt-monitor": {
      name: "MqttMonitorPage",
      module: "@/modules/workflow/pages/MqttMonitorPage/MqttMonitorPage.js",
      shell: true,
    },
    // "Workflow Templates" menu (menu-base-workflow.xml route_path): the template gallery only.
    "/web/workflow/templates": {
      name: "WorkflowTemplatesPage",
      module: "@/modules/workflow/pages/WorkflowTemplatesPage/WorkflowTemplatesPage.js",
      css: "./modules/workflow/pages/WorkflowCanvasPage/WorkflowCanvasPage.css",
      shell: true,
    },
  },
};
