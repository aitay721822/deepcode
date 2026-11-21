FROM eclipse-temurin:21-jdk-noble@sha256:a09e64f4976df9c4f2029c6abe64fc26f069a342ec9a7f147dce7b1933ebda16
RUN curl -fL https://maven-central.storage-download.googleapis.com/maven2/org/scip-code/scip-javac/0.14.2/scip-javac-0.14.2.jar -o /opt/scip-javac.jar \
    && echo '1c7ad1c8361f6ea9f9758e4b55decd4869b0ac7bb8610127aca045173ff590ef  /opt/scip-javac.jar' | sha256sum -c - \
    && curl -fL https://github.com/scip-code/scip-java/releases/download/v0.14.2/scip-java-v0.14.2 -o /opt/scip-java \
    && echo '6620a8f2af822ccfa23fbf8a852560edd05c3834dc8cf779ca507abce71586dc  /opt/scip-java' | sha256sum -c -
