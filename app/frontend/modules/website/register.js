// Frontend registration for website: the "New page" template picker used by the page list
// (`<list create_widget="website_page_template_picker">`). Discovered through GET /frontend-modules.json.
export default {
  formWidgets: {
    website_page_template_picker: {
      module: "@/modules/website/widgets/PageTemplatePicker/PageTemplatePicker.js",
      export: "PageTemplatePicker",
    },
  },
};
