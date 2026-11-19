const template = await (await fetch(new URL("./SchedRenamed.xml", import.meta.url))).text();

export class SchedRenamed extends owl.Component {
  static template = owl.xml({ raw: [template] });
}
