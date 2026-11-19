const template = await (await fetch(new URL("./Home.xml", import.meta.url))).text();

export class Home extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
