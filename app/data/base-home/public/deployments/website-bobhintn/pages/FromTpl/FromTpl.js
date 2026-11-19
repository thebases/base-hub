const template = await (await fetch(new URL("./FromTpl.xml", import.meta.url))).text();

export class FromTpl extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
