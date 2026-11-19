const template = await (await fetch(new URL("./B4Test.xml", import.meta.url))).text();

export class B4Test extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
