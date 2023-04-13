#  Copyright 2023 Gravitational, Inc.
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.
#
import json
import os
import subprocess

import yaml
from langchain.document_loaders import DirectoryLoader, TextLoader
from langchain.embeddings.openai import OpenAIEmbeddings
from langchain.vectorstores import Qdrant


def main():
    if not os.path.exists("documents"):
        os.mkdir("documents")

    # Get all nodes
    output = subprocess.check_output(["tctl", "get", "nodes"], stderr=subprocess.DEVNULL)
    nodes = yaml.safe_load_all(output)

    # Prepare data
    for node in nodes:
        name = node["metadata"]["name"]

        # remove rotation
        node["spec"].pop("rotation", None)

        print(f"✅  Saving node {name}")  # Use emoji to break non UTF-8 compatible interpreters and terminals

        with open(f"documents/{name}.json", "w") as f:
            # Convert to JSON as YAML loses context after removing white-spaces
            payload = json.dumps(node)
            f.write(payload)

    # Ingest part
    loader = DirectoryLoader('./documents',
                             glob='*.json',
                             loader_cls=TextLoader  # Force text loader as JSON loader is not able to read JSONs. 🤯
                             )
    documents = loader.load()
    embeddings = OpenAIEmbeddings(openai_api_key=os.environ['OPENAI_API_KEY'])
    # Load nodes into the DB
    Qdrant.from_documents(documents, embeddings, collection_name="nodes")


if __name__ == '__main__':
    main()
