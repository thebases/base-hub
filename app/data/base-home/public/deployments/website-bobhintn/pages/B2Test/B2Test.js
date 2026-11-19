const template = await (await fetch(new URL("./B2Test.xml", import.meta.url))).text();

export class B2Test extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
