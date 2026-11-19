const template = await (await fetch(new URL("./AboutTr.xml", import.meta.url))).text();

export class AboutTr extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
