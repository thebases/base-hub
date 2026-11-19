const template = await (await fetch(new URL("./LegacyOne.xml", import.meta.url))).text();

export class LegacyOne extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
