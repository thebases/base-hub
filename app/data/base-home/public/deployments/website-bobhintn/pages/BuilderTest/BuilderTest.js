const template = await (await fetch(new URL("./BuilderTest.xml", import.meta.url))).text();

export class BuilderTest extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
