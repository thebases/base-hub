// Frontend registration for website-blog: the rich-text editor behind `<widget name="blog_content_editor"/>`
// on the post form. Discovered through GET /frontend-modules.json (see ../module-registry.js).
export default {
  formWidgets: {
    blog_content_editor: {
      module: "@/modules/website-blog/widgets/BlogContentEditor/BlogContentEditor.js",
      export: "BlogContentEditor",
    },
  },
};
